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
type Event struct {
	Topic string
	ID    string
	Data  []byte
}

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
type subscription struct {
	topics map[string]struct{} // empty = subscribe-all
	ch     chan Event
	// droppedSinceWarn counts events lost to drop-oldest for THIS
	// subscriber. When non-zero the next successful send is preceded
	// by a synthetic "dropped" event so the client can react.
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
		h.mu.Lock()
		if _, ok := h.subs[s]; ok {
			delete(h.subs, s)
			close(s.ch)
		}
		h.mu.Unlock()
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
// oldest event (non-blocking — racing with the subscriber reader),
// increments droppedSinceWarn, and retries. If the second send
// also fails (subscriber drained between our drain and retry —
// race we can't win), we drop the event silently and count it.
//
// If droppedSinceWarn > 0 and the next successful send goes through,
// we prepend a synthetic "dropped" event with the count so the
// client can refetch.
func (h *Hub) deliverTo(s *subscription, e Event) {
	// Drain & retry up to once. Two iterations is enough: if we lost
	// twice in a row, the subscriber is so far behind that one more
	// drop won't help. Count and move on.
	for i := 0; i < 2; i++ {
		// Prepend the dropped-warning event if we owe one. Try
		// non-blocking; if it doesn't fit either, defer to the
		// next call.
		if s.droppedSinceWarn.Load() > 0 {
			lost := s.droppedSinceWarn.Load()
			select {
			case s.ch <- Event{Topic: "dropped", Data: countBytes(lost)}:
				s.droppedSinceWarn.Store(0)
			default:
				// Still no room; the real event below will also
				// fail and we'll loop.
			}
		}
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
