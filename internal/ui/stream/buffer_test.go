package stream

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestHub_SubscribeWithReplay_DrainsBufferedEvents pins the
// reason this whole machinery exists: a late subscriber must
// receive events that were published BEFORE its Subscribe call.
//
// Without the buffer, the create-instance flow would lose the
// first 1-3 step events between POST returning 202 and the
// browser's EventSource open completing.
func TestHub_SubscribeWithReplay_DrainsBufferedEvents(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:demo"
	h.EnableBuffering(topic, 16)

	// Publish 3 events BEFORE any subscriber exists.
	for _, id := range []string{"a", "b", "c"} {
		h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: id})
	}

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()

	// Drain the channel and verify order matches publish order.
	got := drainN(t, ch, 3, time.Second)
	want := []string{"a", "b", "c"}
	for i, ev := range got {
		if ev.ID != want[i] {
			t.Errorf("event %d: ID=%q, want %q", i, ev.ID, want[i])
		}
	}
}

// TestHub_SubscribeWithReplay_LiveEventsFollowReplay pins the
// ordering guarantee: events buffered BEFORE Subscribe come first,
// in publish order, immediately followed by events published
// AFTER Subscribe.
//
// A subtle race here: if Publish ran while SubscribeWithReplay
// was filling the channel, the new event could land before the
// last replayed one. The implementation prevents this by holding
// h.mu.Lock for the full snapshot+register+prefill sequence.
func TestHub_SubscribeWithReplay_LiveEventsFollowReplay(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:demo"
	h.EnableBuffering(topic, 16)
	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "before-1"})
	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "before-2"})

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()

	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "after-1"})
	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "after-2"})

	got := drainN(t, ch, 4, time.Second)
	want := []string{"before-1", "before-2", "after-1", "after-2"}
	for i, ev := range got {
		if ev.ID != want[i] {
			t.Errorf("event %d: ID=%q, want %q (full order: %v)",
				i, ev.ID, want[i], idsOf(got))
		}
	}
}

// TestHub_SubscribeWithReplay_NoBufferYieldsLiveOnly verifies the
// happy degradation path: a topic without EnableBuffering can
// still be subscribed-with-replay, just with an empty snapshot.
// SubscribeWithReplay is then equivalent to Subscribe.
//
// This matters for the create-instance handler: the
// EnableBuffering call happens at the start of the up goroutine.
// If a browser subscribes to a never-buffered topic by typo, the
// stream still works.
func TestHub_SubscribeWithReplay_NoBufferYieldsLiveOnly(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:unknown"
	// Deliberately NO EnableBuffering call.
	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "pre"}) // lost

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()

	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "post"})

	got := drainN(t, ch, 1, time.Second)
	if len(got) != 1 || got[0].ID != "post" {
		t.Errorf("got %v, want [post]", idsOf(got))
	}
}

// TestHub_EnableBuffering_RingEvictsOldest pins the bounded-memory
// contract: a long-running buffered topic keeps only the most
// recent `cap` events. An up that emits 200 events with a cap=128
// buffer must yield exactly 128 on replay, with the oldest 72
// evicted.
func TestHub_EnableBuffering_RingEvictsOldest(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:long"
	h.EnableBuffering(topic, 4)
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: id})
	}

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()

	got := drainN(t, ch, 4, time.Second)
	want := []string{"c", "d", "e", "f"} // a,b evicted
	for i, ev := range got {
		if ev.ID != want[i] {
			t.Errorf("event %d: ID=%q, want %q", i, ev.ID, want[i])
		}
	}
}

// TestHub_ClearBuffer_DropsReplayEvents pins the explicit-cleanup
// contract: after ClearBuffer, the topic's ring is gone. A
// SubscribeWithReplay AFTER the clear receives only live events.
//
// The create-instance handler calls ClearBuffer after the up
// finishes (success or failure) — this test simulates a browser
// that opens the events URL minutes after the up finished, e.g.
// after a back-button navigation. The user shouldn't see stale
// completion events.
func TestHub_ClearBuffer_DropsReplayEvents(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:cleared"
	h.EnableBuffering(topic, 8)
	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "during"})
	h.ClearBuffer(topic)

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()

	// No events should arrive — none in replay (cleared), none live.
	select {
	case ev := <-ch:
		t.Errorf("unexpected event after ClearBuffer: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

// TestHub_EnableBuffering_IsIdempotent verifies the create-instance
// handler's retry-safety: if a POST handler races with a second
// POST (or just re-invokes EnableBuffering defensively), the
// second call is a no-op and doesn't drop already-queued events.
func TestHub_EnableBuffering_IsIdempotent(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:idem"
	h.EnableBuffering(topic, 8)
	h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: "first"})

	// Re-enable with the SAME cap — must not drop the queued event.
	h.EnableBuffering(topic, 8)

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()

	got := drainN(t, ch, 1, time.Second)
	if len(got) != 1 || got[0].ID != "first" {
		t.Errorf("got %v, want [first] after idempotent EnableBuffering", idsOf(got))
	}
}

// TestHub_EnableBuffering_ResizeKeepsRecent verifies a re-enable
// with a DIFFERENT cap shrinks the ring and keeps the most recent
// events. Not a critical UX path — included as defence so a
// future contributor who tunes the cap doesn't accidentally lose
// older events without realising.
func TestHub_EnableBuffering_ResizeKeepsRecent(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:resize"
	h.EnableBuffering(topic, 4)
	for _, id := range []string{"a", "b", "c", "d"} {
		h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: id})
	}
	// Shrink to 2 — should keep c, d.
	h.EnableBuffering(topic, 2)

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()
	got := drainN(t, ch, 2, time.Second)
	want := []string{"c", "d"}
	for i, ev := range got {
		if ev.ID != want[i] {
			t.Errorf("event %d: ID=%q, want %q", i, ev.ID, want[i])
		}
	}
}

// TestHub_TopicIsolation_ReplayDoesntCrossTopics pins that
// instance-A's events never appear in instance-B's replay
// stream. A bug here would leak progress events between
// concurrent ups — annoying at best, potentially confusing
// (browser thinks demo's status is what hubble actually did).
func TestHub_TopicIsolation_ReplayDoesntCrossTopics(t *testing.T) {
	h := New()
	defer h.Close()

	h.EnableBuffering("instance:a", 8)
	h.EnableBuffering("instance:b", 8)

	h.Publish(Event{SchemaVersion: 1, Topic: "instance:a", ID: "a1"})
	h.Publish(Event{SchemaVersion: 1, Topic: "instance:b", ID: "b1"})
	h.Publish(Event{SchemaVersion: 1, Topic: "instance:a", ID: "a2"})

	chA, cancelA := h.SubscribeWithReplay("instance:a")
	defer cancelA()
	chB, cancelB := h.SubscribeWithReplay("instance:b")
	defer cancelB()

	gotA := drainN(t, chA, 2, time.Second)
	gotB := drainN(t, chB, 1, time.Second)

	if ids := idsOf(gotA); ids[0] != "a1" || ids[1] != "a2" {
		t.Errorf("topic A replay = %v, want [a1 a2]", ids)
	}
	if ids := idsOf(gotB); ids[0] != "b1" {
		t.Errorf("topic B replay = %v, want [b1]", ids)
	}
}

// TestHub_SubscribeWithReplay_RaceWithPublisher tries to force the
// race the implementation guards against: a publisher writes to
// the topic buffer while SubscribeWithReplay is mid-snapshot. The
// guarantee is that NO event appears twice on the subscriber's
// channel and NONE is dropped (the subscriber sees a strict prefix
// of the publish stream followed by all subsequent events).
func TestHub_SubscribeWithReplay_RaceWithPublisher(t *testing.T) {
	h := New()
	defer h.Close()

	const topic = "instance:race"
	h.EnableBuffering(topic, 64)

	// Start a publisher firing events as fast as possible.
	ctx, cancelPub := context.WithCancel(context.Background())
	defer cancelPub()
	var publishedIDs sync.Map
	go func() {
		i := uint64(0)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Monotonic unique IDs — the "no duplicates"
				// assertion below depends on IDs never repeating.
				id := strconv.FormatUint(i, 10)
				h.Publish(Event{SchemaVersion: 1, Topic: topic, ID: id})
				publishedIDs.Store(id, i)
				i++
			}
		}
	}()

	// Subscribe a bit later (give the publisher a head start).
	time.Sleep(2 * time.Millisecond)

	ch, cancel := h.SubscribeWithReplay(topic)
	defer cancel()

	// Drain for another moment, then stop the publisher.
	deadline := time.After(5 * time.Millisecond)
	got := []Event{}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			got = append(got, ev)
		case <-deadline:
			goto done
		}
	}
done:
	cancelPub()

	// Check: every event we got was actually published, and no
	// event appears twice. Filter out the synthetic Topic="dropped"
	// events the hub emits when drop-oldest fires — those carry
	// empty IDs by design and aren't part of the publish stream.
	seen := make(map[string]bool, len(got))
	for _, ev := range got {
		if ev.Topic == "dropped" {
			continue
		}
		if _, ok := publishedIDs.Load(ev.ID); !ok {
			t.Errorf("subscriber got unpublished event %q", ev.ID)
		}
		if seen[ev.ID] {
			t.Errorf("subscriber got duplicate event %q", ev.ID)
		}
		seen[ev.ID] = true
	}
	if len(got) == 0 {
		t.Error("expected at least some events, got none")
	}
}

func drainN(t *testing.T, ch <-chan Event, n int, timeout time.Duration) []Event {
	t.Helper()
	got := make([]Event, 0, n)
	deadline := time.After(timeout)
	for len(got) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed early at %d/%d events", len(got), n)
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout waiting for event %d/%d; got so far: %v",
				len(got)+1, n, idsOf(got))
		}
	}
	return got
}

func idsOf(events []Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.ID
	}
	return out
}
