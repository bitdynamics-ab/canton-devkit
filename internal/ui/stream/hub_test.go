package stream

import (
	"sync"
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
func TestHub_DroppedEventPrependedAfterBackpressure(t *testing.T) {
	h := NewWithBuffer(2)
	ch, cancel := h.Subscribe()
	defer cancel()

	// Fill + overflow the buffer.
	for i := 0; i < 5; i++ {
		h.Publish(Event{Topic: "x", Data: []byte{byte('0' + i)}})
	}

	// Drain. Somewhere in this stream we should see the "dropped"
	// event.
	sawDropped := false
	for i := 0; i < 5; i++ {
		select {
		case e := <-ch:
			if e.Topic == "dropped" {
				sawDropped = true
			}
		case <-time.After(200 * time.Millisecond):
			break
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
