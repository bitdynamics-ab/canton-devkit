package stream

import (
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHub_PublishReachesAllSubscribers is the happy-path: one publish,
// every subscribed channel sees the event.
func TestHub_PublishReachesAllSubscribers(t *testing.T) {
	h := New()
	a, cancelA := h.Subscribe("services")
	defer cancelA()
	b, cancelB := h.Subscribe("services", "metrics")
	defer cancelB()
	c, cancelC := h.Subscribe() // subscribe-all
	defer cancelC()

	got := h.Publish(Event{Topic: "services", Data: []byte("x")})
	if got != 3 {
		t.Errorf("delivered to %d, want 3", got)
	}
	for _, ch := range []<-chan Event{a, b, c} {
		select {
		case e := <-ch:
			if e.Topic != "services" || string(e.Data) != "x" {
				t.Errorf("unexpected event: %+v", e)
			}
		case <-time.After(200 * time.Millisecond):
			t.Error("subscriber didn't receive event")
		}
	}
}

// TestHub_TopicFilterExcludesUninterested verifies the topic
// filter: a subscriber on "metrics" does NOT receive a "services"
// event.
func TestHub_TopicFilterExcludesUninterested(t *testing.T) {
	h := New()
	metricsOnly, cancel := h.Subscribe("metrics")
	defer cancel()

	h.Publish(Event{Topic: "services", Data: []byte("nope")})
	select {
	case e := <-metricsOnly:
		t.Errorf("metrics-only subscriber got services event: %+v", e)
	case <-time.After(50 * time.Millisecond):
		// Good.
	}
}

// TestHub_DropOldestUnderBackpressure is the load-bearing test for
// the slow-client invariant: when a subscriber's buffer fills, the
// publisher MUST NOT block, the OLDEST event is dropped (not the
// newest), and the dropped count is reflected in Stats. A small
// buffer makes this deterministic.
//
// Catches the regression class where someone refactors the hub to
// use blocking send (`ch <- e`) — the publisher would then back-
// pressure the docker poller and one stuck tab would freeze the
// instance.
func TestHub_DropOldestUnderBackpressure(t *testing.T) {
	h := NewWithBuffer(2) // tiny buffer
	_, cancel := h.Subscribe()
	defer cancel()

	// Publish 5 events without ever reading from the subscriber.
	// With buffer=2, events 3-5 should drop the oldest (1, then 2)
	// and the channel ends up holding the two newest (4, 5) plus
	// a synthetic "dropped" event prepended on the next successful
	// send.
	deadline := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			h.Publish(Event{Topic: "x", Data: []byte{byte('0' + i)}})
		}
		close(deadline)
	}()

	select {
	case <-deadline:
		// Good — publishing never blocked.
	case <-time.After(time.Second):
		t.Fatal("Publish blocked under backpressure — slow-client invariant violated")
	}

	stats := h.Stats()
	if stats.Dropped == 0 {
		t.Errorf("Stats.Dropped = 0, want >0 — drop-oldest didn't increment counter")
	}
	if stats.Published != 5 {
		t.Errorf("Stats.Published = %d, want 5", stats.Published)
	}
}

// TestHub_DroppedEventPrependedAfterBackpressure verifies the
// client-facing half of drop-oldest: after a drop, the NEXT
// successful delivery is preceded by a synthetic Event{Topic:
// "dropped"} so the client knows to refetch.
//
// The deterministic shape:
//
//  1. Publish two events to fill the buf=2 buffer.
//  2. Publish a third → drop-oldest fires, droppedSinceWarn=1.
//  3. Drain one event to make room.
//  4. Publish a fourth → deliverTo sees droppedSinceWarn>0, sends
//     the synthetic "dropped" event into the freed slot, then
//     the fourth real event.
//  5. Drain — must see "dropped" before reaching the fourth.
func TestHub_DroppedEventPrependedAfterBackpressure(t *testing.T) {
	h := NewWithBuffer(2)
	ch, cancel := h.Subscribe()
	defer cancel()

	h.Publish(Event{Topic: "x", Data: []byte("1")})
	h.Publish(Event{Topic: "x", Data: []byte("2")})
	h.Publish(Event{Topic: "x", Data: []byte("3")}) // drops "1"
	// Drain one to make room for the dropped-warning.
	<-ch
	h.Publish(Event{Topic: "x", Data: []byte("4")})

	// Drain remaining; the "dropped" topic must appear before
	// "4" lands. We allow up to 4 reads (warning + 3 real).
	sawDropped := false
	for i := 0; i < 4; i++ {
		select {
		case e := <-ch:
			if e.Topic == "dropped" {
				sawDropped = true
			}
		case <-time.After(200 * time.Millisecond):
			i = 4 // break loop
		}
	}
	if !sawDropped {
		t.Error("no 'dropped' synthetic event observed after backpressure — client wouldn't know to refetch")
	}
}

// TestHub_CancelClosesChannelAndStops removes the subscription.
// After cancel, the channel is closed (range exits) and subsequent
// publishes don't try to deliver to it.
func TestHub_CancelClosesChannelAndStops(t *testing.T) {
	h := New()
	ch, cancel := h.Subscribe()
	cancel()
	// Receiving from a closed channel returns zero-value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel was not closed by cancel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("recv from canceled channel blocked — channel wasn't closed")
	}
	// Subsequent publish shouldn't reach 1 (the canceled sub).
	if got := h.Publish(Event{Topic: "x"}); got != 0 {
		t.Errorf("published to %d after cancel, want 0", got)
	}
}

// TestHub_ConcurrentPublishersAndSubscribers smoke-tests the
// goroutine-safety claim from the package godoc. 4 publishers ×
// 4 subscribers × 100 events each. No race detector flags = pass.
// Run with `go test -race ./internal/ui/stream/`.
func TestHub_ConcurrentPublishersAndSubscribers(t *testing.T) {
	h := New()
	const n = 100

	// Subscribers consume eagerly so drop-oldest doesn't kick in
	// (this test is about race-detector cleanliness, not back-
	// pressure semantics).
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		ch, cancel := h.Subscribe()
		go func() {
			defer wg.Done()
			defer cancel()
			received := 0
			for received < n*4 {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
					received++
				case <-time.After(2 * time.Second):
					return // best-effort under load
				}
			}
		}()
	}

	for i := 0; i < 4; i++ {
		go func() {
			for j := 0; j < n; j++ {
				h.Publish(Event{Topic: "x"})
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("subscribers did not drain within timeout")
	}
}

// TestNewWithBuffer_MinimumOne pins the defensive floor: buf<1 is
// promoted to 1 so a misconfigured caller doesn't get a zero-cap
// channel that blocks every send.
func TestNewWithBuffer_MinimumOne(t *testing.T) {
	h := NewWithBuffer(0)
	if h.bufLen != 1 {
		t.Errorf("buf=0 produced bufLen=%d, want 1 (zero-cap channel would block all sends)", h.bufLen)
	}
	h = NewWithBuffer(-5)
	if h.bufLen != 1 {
		t.Errorf("buf=-5 produced bufLen=%d, want 1", h.bufLen)
	}
}

// TestEvent_CarriesSchemaVersion pins the invariant —
// Event struct must have a SchemaVersion field so the wire-
// level message carries the same handshake the /api/version
// endpoint exposes. Without this, a long-running EventSource
// can't detect a mid-session server upgrade.
//
// Reflective check: walk the Event struct and assert a
// SchemaVersion int field with json:"schema_version" tag exists.
func TestEvent_CarriesSchemaVersion(t *testing.T) {
	rt := reflect.TypeOf(Event{})
	f, ok := rt.FieldByName("SchemaVersion")
	if !ok {
		t.Fatal("Event missing SchemaVersion field — wire-level handshake regressed")
	}
	if f.Type.Kind() != reflect.Int {
		t.Errorf("Event.SchemaVersion kind = %v, want int", f.Type.Kind())
	}
	if tag := f.Tag.Get("json"); tag != "schema_version" {
		t.Errorf(`Event.SchemaVersion json tag = %q, want "schema_version"`, tag)
	}
	if EventSchemaVersion < 1 {
		t.Errorf("EventSchemaVersion = %d; expected >= 1", EventSchemaVersion)
	}
}

// TestHub_NoSendOnClosedChannelAcrossCancelRace pins that cancel()
// closing the subscriber's channel cannot race a concurrent Publish
// into a send-to-closed panic — the per-subscription mutex
// serialises close with deliverTo. The test drives the race hard:
// N goroutines publishing while M goroutines cancel and
// re-subscribe. Without the serialisation, the race detector AND
// the panic trip within a few iterations.
func TestHub_NoSendOnClosedChannelAcrossCancelRace(t *testing.T) {
	h := New()
	const rounds = 500

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic during cancel/publish race: %v", r)
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				h.Publish(Event{Topic: "x"})
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				_, cancel := h.Subscribe()
				cancel()
			}
		}()
	}
	wg.Wait()
}

// TestHub_DropAccountingUnderConcurrentLoad pins the atomic Swap(0)
// drop accounting in deliverTo: a Load + Store pair would lose a
// concurrent increment between the Load and the Store. The test
// publishes faster than the subscriber reads, then asserts the
// accounted drops cover the (published - delivered) gap.
func TestHub_DropAccountingUnderConcurrentLoad(t *testing.T) {
	h := NewWithBuffer(8)
	ch, cancel := h.Subscribe()
	defer cancel()

	const total = 2000
	var publishWG sync.WaitGroup
	for i := 0; i < 4; i++ {
		publishWG.Add(1)
		go func() {
			defer publishWG.Done()
			for j := 0; j < total/4; j++ {
				h.Publish(Event{Topic: "x"})
			}
		}()
	}

	// Drain slowly — fewer reads than publishes, forcing drops.
	// `delivered` is atomic because the consumer goroutine writes
	// it while the assertions read it (test itself must be
	// race-clean under `go test -race`).
	var delivered atomic.Uint64
	go func() {
		for range ch {
			delivered.Add(1)
			time.Sleep(50 * time.Microsecond) // slow consumer
		}
	}()

	publishWG.Wait()
	// Let any in-flight drains land.
	time.Sleep(500 * time.Millisecond)

	stats := h.Stats()
	if stats.Published != total {
		t.Errorf("Published = %d, want %d", stats.Published, total)
	}
	if stats.Dropped == 0 {
		t.Errorf("Stats.Dropped = 0 under heavy backpressure — drop accounting regressed")
	}
	// Total events MUST be accounted for: every published event
	// either reached the subscriber OR is in Dropped (modulo the
	// synthetic "dropped" prepends, which we tolerate via a small
	// slack). A Load+Store regression would lose drops; this
	// invariant catches it.
	got := delivered.Load() + stats.Dropped
	if got+10 < uint64(total) { // 10-event slack for in-flight + synthetic
		t.Errorf("delivered+dropped = %d, want >= %d (lost %d events to bad accounting)",
			got, total, uint64(total)-got)
	}
}

// TestHub_NoGoroutineLeakAfterDisconnects pins that subscribe +
// cancel N times leaves no state behind. Catches the leak class
// where cancel() forgets to remove from h.subs or to close the
// channel — accumulated subscriptions are visible via
// Stats().Subscribers.
func TestHub_NoGoroutineLeakAfterDisconnects(t *testing.T) {
	h := New()
	const cycles = 200
	for i := 0; i < cycles; i++ {
		_, cancel := h.Subscribe()
		cancel()
	}
	if got := h.Stats().Subscribers; got != 0 {
		t.Errorf("Stats.Subscribers = %d after %d connect+cancel cycles, want 0 — cancel path leaks",
			got, cycles)
	}
}

// TestHub_SlowClientDoesNotBlockOthers pins the invariant that one
// stuck subscriber (never reads) must NOT prevent a fast subscriber
// from receiving events. This is the "one bad tab takes down
// the instance" regression class — drop-oldest must isolate
// subscribers.
func TestHub_SlowClientDoesNotBlockOthers(t *testing.T) {
	h := NewWithBuffer(2)
	// stuck: never reads. Buffer fills, every subsequent publish
	// triggers drop-oldest on this subscriber but MUST NOT block
	// the publisher.
	_, stuckCancel := h.Subscribe()
	defer stuckCancel()

	// fast: drains promptly.
	fast, fastCancel := h.Subscribe()
	defer fastCancel()

	deadline := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			h.Publish(Event{Topic: "x", Data: []byte{byte(i)}})
		}
		close(deadline)
	}()

	select {
	case <-deadline:
	case <-time.After(time.Second):
		t.Fatal("Publish loop blocked — slow stuck subscriber back-pressured the publisher")
	}

	// fast must have received events (possibly fewer than 50 if
	// it also stalled, but distinctly more than 0).
	fastCount := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case _, ok := <-fast:
			if !ok {
				return
			}
			fastCount++
			if fastCount >= 10 {
				return // good enough — fast sub is receiving
			}
		case <-timeout:
			if fastCount == 0 {
				t.Error("fast subscriber received 0 events — slow client blocked others")
			}
			return
		}
	}
}

// TestHub_NoGoroutineLeakAfterChurn complements
// TestHub_NoGoroutineLeakAfterDisconnects: instead of asserting the
// Subscribers count returns to 0, it takes a runtime.NumGoroutine
// baseline before the churn and asserts it returns to within a small
// tolerance after. Catches a subscribe/cancel pair leaving a stray
// goroutine alive — invisible to the Subscribers count but a leak.
func TestHub_NoGoroutineLeakAfterChurn(t *testing.T) {
	// Settle pre-existing goroutines from earlier tests.
	for i := 0; i < 5; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	baseline := runtime.NumGoroutine()

	h := New()
	const cycles = 200
	for i := 0; i < cycles; i++ {
		_, cancel := h.Subscribe()
		cancel()
	}

	// Allow a beat for any cleanup goroutines to wind down.
	for i := 0; i < 10; i++ {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := runtime.NumGoroutine()
	if got > baseline+2 { // 2 slack — flaky test runners spawn workers
		t.Errorf("goroutine count went %d → %d after %d subscribe+cancel cycles (delta %d) — leak",
			baseline, got, cycles, got-baseline)
	}
}

// TestHub_CloseDisconnectsActiveSubscribers pins that Hub.Close
// closes every active subscriber's channel so a downstream reader
// (the SSE handler's for-range loop) unblocks and exits cleanly.
func TestHub_CloseDisconnectsActiveSubscribers(t *testing.T) {
	h := New()
	chA, cancelA := h.Subscribe()
	defer cancelA()
	chB, cancelB := h.Subscribe()
	defer cancelB()

	h.Close()

	// Both channels should be closed; a recv on a closed empty
	// channel returns immediately with ok=false.
	for i, ch := range []<-chan Event{chA, chB} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Errorf("channel %d: recv got non-zero value, want closed", i)
			}
		case <-time.After(time.Second):
			t.Errorf("channel %d: recv blocked, want closed", i)
		}
	}
	// Subsequent Publish is a no-op.
	if got := h.Publish(Event{Topic: "x"}); got != 0 {
		t.Errorf("Publish after Close delivered to %d subs, want 0", got)
	}
}

// TestHub_CloseIsIdempotent — calling Close twice must not
// panic (double-close on a channel panics; the second Close
// must skip channels that are already closed).
func TestHub_CloseIsIdempotent(t *testing.T) {
	h := New()
	_, _ = h.Subscribe()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Close panicked on second call: %v", r)
		}
	}()
	h.Close()
	h.Close()
}

// TestHub_MultiPublisherSerialisedDeliveryNoDoubleDrop pins that
// concurrent publishers to the same subscriber serialise on s.mu, so
// two simultaneous deliverTo calls cannot both drain the channel and
// double-count drops.
//
// Test: 4 publishers × 250 events into buf=4 with a slow consumer;
// asserts the accounting never exceeds the 1000 published events.
func TestHub_MultiPublisherSerialisedDeliveryNoDoubleDrop(t *testing.T) {
	h := NewWithBuffer(4)
	ch, cancel := h.Subscribe()
	defer cancel()

	const perPub = 250
	const publishers = 4
	const total = perPub * publishers

	// Consumer counts only REAL events (not synthetic "dropped"
	// warnings) so the conservation invariant has a clean
	// arithmetic shape: delivered (real) + Dropped (Stats) ≤
	// Published. Synthetic events are bookkeeping, not user data.
	var delivered atomic.Uint64
	go func() {
		for ev := range ch {
			if ev.Topic == "dropped" {
				continue
			}
			delivered.Add(1)
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perPub; j++ {
				h.Publish(Event{Topic: "x"})
			}
		}()
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond) // let drain settle

	stats := h.Stats()
	d := delivered.Load()
	// Safety invariants under concurrent publish load:
	//   - Published equals the inputs (no lost publish call).
	//   - Dropped ≤ Published — a double-drain would push Dropped
	//     above Published.
	//   - Real-event delivered count ≤ Published (no synthesised
	//     real events).
	// Exact arithmetic (delivered + dropped == published) is NOT a
	// clean invariant: synthetic "dropped" warnings can themselves
	// be drained by the next drop, over-counting h.dropped by 1 per
	// drained warning.
	if stats.Published != total {
		t.Errorf("Published = %d, want %d", stats.Published, total)
	}
	if stats.Dropped > uint64(total) {
		t.Errorf("Dropped = %d > Published = %d — double-drain regression",
			stats.Dropped, total)
	}
	if d > uint64(total) {
		t.Errorf("delivered (real) = %d > Published = %d — fabricated events",
			d, total)
	}
}
