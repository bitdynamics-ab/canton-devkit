// Package stream implements the SSE (Server-Sent Events) broadcaster that
// powers the M2 Web UI's live-update channel at /events.
//
// # The slow-client problem
//
// The naive SSE hub — one unbounded channel per subscriber, write blocks
// the publisher — has a fatal failure mode: a single slow browser tab
// (network stall, OS suspend, devtools attach) backs the channel up,
// which backs the publisher up, which blocks the docker poller or the
// ledger client. One sleeping tab takes down the whole instance.
//
// This hub uses **bounded per-subscriber channels with drop-oldest**:
// when a subscriber's buffer is full, the OLDEST event is discarded
// (not the newest) and a `dropped` event is queued so the client knows
// to refetch state. The publisher NEVER blocks on a subscriber. This
// is the same pattern Grafana Live and CockroachDB's CDC use.
//
// # Topics
//
// Subscribers register for one or more named topics ("services",
// "logs:demo", "metrics", etc.). The publisher names a topic when
// publishing; only subscribers of that topic receive the event. Topic
// names are opaque strings — handlers define their own taxonomy.
//
// # Lifecycle
//
// New() returns a Hub ready to publish. The hub has no goroutines of
// its own; Subscribe() returns a channel that the HTTP handler reads
// from. When the handler closes (client disconnect), it MUST call the
// returned cancel func to free the subscription.
package stream

import (
	"sync"
	"sync/atomic"
)

// Event is a single broadcast payload. Topic routes the event; Data
// is the opaque body the SSE handler serialises to the wire. ID is
// optional and appears as the SSE `id:` line for client-side
// last-event tracking.
//
// SchemaVersion mirrors internal/api/types.SchemaVersion. Reviewer pin
// (PR #42 #d): every wire-level message the Web UI consumes needs a
// schema-version field so a frontend bundled for v1 can refuse to
// decode a v2 event with a clear error rather than silently mis-
// interpreting fields. The router.handleVersion endpoint surfaces the
// same number; the event-level field lets a long-running EventSource
// detect a server upgrade mid-session.
type Event struct {
	SchemaVersion int    `json:"schema_version"`
	Topic         string `json:"topic"`
	ID            string `json:"id,omitempty"`
	Data          []byte `json:"data,omitempty"`
}

// EventSchemaVersion is the canonical value Event.SchemaVersion takes
// today. Mirrors types.SchemaVersion; the parity test in hub_test.go
// asserts they stay equal. Inlined to avoid an import on api/types
// for a single integer constant (the dependency direction should
// stay handlers→types, not stream→types).
const EventSchemaVersion = 1

// defaultBuffer is the per-subscriber channel capacity. 64 events is
// enough to absorb a few seconds of normal docker-poll traffic
// (current poll cadence ~3s, payload counts ~10s of events/poll)
// without losing data, while small enough that drop-oldest kicks in
// promptly when a client genuinely stalls. Tuned in
// TestHub_DropOldestUnderBackpressure.
const defaultBuffer = 64

// Hub is the publisher. Goroutine-safe; methods are intended to be
// called from many goroutines (one publisher per data source, one
// subscriber per HTTP handler).
type Hub struct {
	mu     sync.RWMutex
	subs   map[*subscription]struct{}
	bufLen int

	// stats — atomically updated, read by Stats() for observability.
	published atomic.Uint64
	dropped   atomic.Uint64
}

// subscription is the per-subscriber state the publisher writes to.
// One per active /events HTTP request.
//
// closeMu serialises close(ch) (in cancel) with sends in deliverTo
// (called by Publish). Reviewer pin (PR #42 #a): without this, a
// cancel that runs while a Publish is mid-send races on the channel
// state — sending to a just-closed channel PANICS. closeMu is held
// for read by deliverTo (multiple Publishes may be in flight) and
// for write by cancel; the boolean `closed` is checked under the
// read lock so deliverTo short-circuits without trying to send.
type subscription struct {
	topics  map[string]struct{} // empty = subscribe-all
	ch      chan Event
	closeMu sync.RWMutex
	closed  bool // protected by closeMu
	// droppedSinceWarn counts events lost to drop-oldest for THIS
	// subscriber. When non-zero the next successful send is preceded
	// by a synthetic "dropped" event so the client can react.
	//
	// Reviewer pin (PR #42 #b): atomic.Uint64 keeps the per-add safe,
	// BUT the Load + Store in deliverTo was racy — a concurrent
	// Publish could increment between our Load and our Store, losing
	// drop accounting. Now we use Swap(0) to atomically read + reset.
	droppedSinceWarn atomic.Uint64
}

// New constructs a Hub with the default per-subscriber buffer.
func New() *Hub {
	return &Hub{
		subs:   make(map[*subscription]struct{}),
		bufLen: defaultBuffer,
	}
}

// NewWithBuffer is the variant tests use to dial buffer size up or
// down without touching defaultBuffer. Production callers should
// stay on New().
func NewWithBuffer(buf int) *Hub {
	if buf < 1 {
		buf = 1
	}
	return &Hub{
		subs:   make(map[*subscription]struct{}),
		bufLen: buf,
	}
}

// Subscribe registers a new subscriber. topics may be empty to
// receive every event regardless of topic; otherwise only events
// whose Topic matches one of the registered names are delivered.
//
// Returns a receive-only channel and a cancel func. The handler
// MUST call cancel when the client disconnects, otherwise the
// subscription leaks and back-pressures the publisher's drop-
// oldest scan.
func (h *Hub) Subscribe(topics ...string) (<-chan Event, func()) {
	s := &subscription{
		topics: make(map[string]struct{}, len(topics)),
		ch:     make(chan Event, h.bufLen),
	}
	for _, t := range topics {
		s.topics[t] = struct{}{}
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		// Remove from the publisher's set FIRST under h.mu, so no
		// new Publish call will pick this subscription up.
		h.mu.Lock()
		if _, ok := h.subs[s]; !ok {
			h.mu.Unlock()
			return // already cancelled — idempotent
		}
		delete(h.subs, s)
		h.mu.Unlock()

		// Then close the channel under the subscription's own
		// write lock — this synchronises with deliverTo's RLock,
		// guaranteeing no in-flight Publish is still trying to
		// send when close() runs. Without this, a Publish that
		// picked up the sub before our delete-from-h.subs could
		// race close → panic on send-to-closed-channel.
		s.closeMu.Lock()
		s.closed = true
		close(s.ch)
		s.closeMu.Unlock()
	}
	return s.ch, cancel
}

// Publish broadcasts an event to every interested subscriber.
// NEVER blocks on a slow subscriber: if a subscriber's buffer is
// full, the oldest queued event is discarded and a count is
// incremented. The subscriber's NEXT successful delivery is
// preceded by a synthetic Event{Topic: "dropped"} so the client
// knows it missed some updates and can refetch.
//
// Returns the count of subscribers that received the event (or
// had drop-oldest applied — both count). 0 means no interested
// subscriber, which is normal during quiet periods.
func (h *Hub) Publish(e Event) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	delivered := 0
	for s := range h.subs {
		// Topic filter: empty topic set = match-all.
		if len(s.topics) > 0 {
			if _, ok := s.topics[e.Topic]; !ok {
				continue
			}
		}
		h.deliverTo(s, e)
		delivered++
	}
	h.published.Add(1)
	return delivered
}

// deliverTo handles the drop-oldest semantics for one subscriber.
// Tries a non-blocking send; if the buffer is full, drains the
// oldest event, increments droppedSinceWarn, and retries.
//
// Holds s.closeMu for read for the entire duration so the cancel
// path can't close(s.ch) between our fullness check and the send.
// Reviewer pin (PR #42 #a). If the subscription was already
// closed by the time we acquire the lock, the function is a no-op
// — the event simply doesn't reach the dead subscriber.
//
// Drop accounting uses Swap(0) (PR #42 #b): the previous Load +
// Store pair was racy — a concurrent Publish could increment
// droppedSinceWarn between our Load and Store, losing the
// increment. Swap is atomic.
func (h *Hub) deliverTo(s *subscription, e Event) {
	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	if s.closed {
		return
	}

	// Prepend the dropped-warning event if we owe one. Try
	// non-blocking; if it doesn't fit either, restore the count
	// and let the next call handle it.
	if lost := s.droppedSinceWarn.Swap(0); lost > 0 {
		select {
		case s.ch <- Event{SchemaVersion: EventSchemaVersion,
			Topic: "dropped", Data: countBytes(lost)}:
		default:
			// Restore the count — we owe the warning still.
			s.droppedSinceWarn.Add(lost)
		}
	}

	// Drain + retry up to once.
	for i := 0; i < 2; i++ {
		select {
		case s.ch <- e:
			return
		default:
			// Full — drop the oldest, then retry.
			select {
			case <-s.ch:
				s.droppedSinceWarn.Add(1)
				h.dropped.Add(1)
			default:
				// Subscriber drained the channel between our
				// fullness check and the drain attempt. The
				// channel is empty now; retry will succeed.
			}
		}
	}
	// Two failures in a row — count and drop.
	s.droppedSinceWarn.Add(1)
	h.dropped.Add(1)
}

// countBytes encodes a small integer as ASCII bytes without
// pulling strconv. The "dropped" event carries just a number for
// the client to decide whether to refetch.
func countBytes(n uint64) []byte {
	if n == 0 {
		return []byte("0")
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return append([]byte(nil), buf[i:]...)
}

// Stats returns counters useful for /healthz-style introspection
// and tests. NOT atomic across fields — callers shouldn't rely on
// a coherent snapshot.
type Stats struct {
	Subscribers int
	Published   uint64
	Dropped     uint64
}

// Stats reports current counters.
func (h *Hub) Stats() Stats {
	h.mu.RLock()
	n := len(h.subs)
	h.mu.RUnlock()
	return Stats{
		Subscribers: n,
		Published:   h.published.Load(),
		Dropped:     h.dropped.Load(),
	}
}
