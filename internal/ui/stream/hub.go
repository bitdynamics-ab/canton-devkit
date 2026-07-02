// Package stream implements the SSE (Server-Sent Events) broadcaster that
// powers the Web UI's live-update channel at /events.
//
// # The slow-client problem
//
// A naive SSE hub — write blocks the publisher — lets a single slow
// browser tab back up the publisher and freeze the docker poller or
// ledger client. This hub instead uses bounded per-subscriber channels
// with drop-oldest: when a subscriber's buffer is full, the OLDEST
// event is discarded and a `dropped` event is queued so the client
// knows to refetch state. The publisher never blocks on a subscriber.
//
// # Topics
//
// Subscribers register for one or more named topics ("services",
// "logs:demo", …); only events published on a matching topic are
// delivered. Topic names are opaque strings — handlers define their
// own taxonomy.
//
// # Lifecycle
//
// New() returns a Hub ready to publish. The hub has no goroutines of
// its own; Subscribe() returns a channel that the HTTP handler reads
// from. When the handler closes (client disconnect), it MUST call the
// returned cancel func to free the subscription.
package stream

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Event is a single broadcast payload. Topic routes the event; Data
// is the opaque body the SSE handler serialises to the wire. ID is
// optional and appears as the SSE `id:` line for client-side
// last-event tracking.
//
// SchemaVersion mirrors internal/api/types.SchemaVersion. Every
// wire-level message the Web UI consumes carries a schema-version field
// so a frontend bundled for v1 can refuse to decode a v2 event with a
// clear error; the event-level field also lets a long-running
// EventSource detect a server upgrade mid-session.
type Event struct {
	SchemaVersion int    `json:"schema_version"`
	Topic         string `json:"topic"`
	ID            string `json:"id,omitempty"`
	Data          []byte `json:"data,omitempty"`
}

// EventSchemaVersion is the canonical value Event.SchemaVersion takes
// today. Mirrors types.SchemaVersion (duplicated rather than imported
// so the dependency direction stays handlers→types, not stream→types).
const EventSchemaVersion = 1

// defaultBuffer is the per-subscriber channel capacity: large enough
// to absorb a few seconds of normal docker-poll traffic, small enough
// that drop-oldest kicks in promptly when a client genuinely stalls.
const defaultBuffer = 64

// Hub is the publisher. Goroutine-safe; methods are intended to be
// called from many goroutines (one publisher per data source, one
// subscriber per HTTP handler).
type Hub struct {
	mu     sync.RWMutex
	subs   map[*subscription]struct{}
	bufLen int

	// Per-topic event buffers for the replay-on-subscribe contract,
	// populated only for topics opted in via EnableBuffering. The
	// create-flow SSE stream relies on this: the up goroutine
	// publishes its first step events before the browser's
	// EventSource opens, and SubscribeWithReplay drains the buffer
	// so those events aren't lost. Buffers are removed on
	// ClearBuffer(topic).
	topicBuffers map[string]*topicBuffer

	// stats — atomically updated, read by Stats() for observability.
	published atomic.Uint64
	dropped   atomic.Uint64
}

// topicBuffer is a fixed-size ring of recent events for one topic.
// A full buffer evicts the oldest event silently — unlike the live
// drop-oldest path there is no "dropped" sentinel, because the lost
// events were published before the subscriber existed.
type topicBuffer struct {
	mu     sync.Mutex
	events []Event
	cap    int
}

func newTopicBuffer(cap int) *topicBuffer {
	if cap < 1 {
		cap = 1
	}
	return &topicBuffer{
		events: make([]Event, 0, cap),
		cap:    cap,
	}
}

// append adds an event to the ring, evicting the oldest when full.
// The shift-left copy is bounded by `cap`, negligible at the
// human-paced rate of step events.
func (b *topicBuffer) append(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= b.cap {
		copy(b.events, b.events[1:])
		b.events = b.events[:len(b.events)-1]
	}
	b.events = append(b.events, e)
}

// snapshot returns a copy of the current buffer contents. Safe to
// hand to subscribers — no aliasing with future appends.
func (b *topicBuffer) snapshot() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Event, len(b.events))
	copy(out, b.events)
	return out
}

// subscription is the per-subscriber state the publisher writes to.
// One per active /events HTTP request.
//
// mu serialises BOTH close(ch) (in cancel) AND the deliverTo body.
// A plain Mutex (not RWMutex) is deliberate: concurrent deliverTo
// calls could both drain the same channel, double-counting drops and
// racing the retry. The Mutex is per-subscription, so other
// subscribers still proceed in parallel; the hub-level RWMutex (h.mu)
// keeps the SET of subscribers safe for concurrent iteration.
type subscription struct {
	topics map[string]struct{} // empty = subscribe-all
	ch     chan Event
	mu     sync.Mutex
	closed bool // protected by mu
	// droppedSinceWarn counts events lost to drop-oldest for THIS
	// subscriber. When non-zero the next successful send is preceded
	// by a synthetic "dropped" event so the client can react.
	// Read+reset via Swap(0) under mu so concurrent increments
	// inside deliverTo are safe.
	droppedSinceWarn atomic.Uint64
}

// New constructs a Hub with the default per-subscriber buffer.
func New() *Hub {
	return &Hub{
		subs:         make(map[*subscription]struct{}),
		bufLen:       defaultBuffer,
		topicBuffers: make(map[string]*topicBuffer),
	}
}

// NewWithBuffer is the variant tests use to dial the per-subscriber
// buffer size. Production callers should stay on New().
func NewWithBuffer(buf int) *Hub {
	if buf < 1 {
		buf = 1
	}
	return &Hub{
		subs:         make(map[*subscription]struct{}),
		bufLen:       buf,
		topicBuffers: make(map[string]*topicBuffer),
	}
}

// EnableBuffering opts a topic into the replay buffer. Subsequent
// Publish calls for `topic` append to a fixed-size ring of `cap`
// events (clamped to at least 1); SubscribeWithReplay drains the ring
// into new subscribers before live event flow begins.
//
// Idempotent — re-enabling with the same cap is a no-op; a different
// cap resizes the existing buffer (truncating oldest-first if
// smaller). The matching ClearBuffer call frees the ring.
func (h *Hub) EnableBuffering(topic string, cap int) {
	if cap < 1 {
		cap = 1
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	existing, ok := h.topicBuffers[topic]
	if !ok {
		h.topicBuffers[topic] = newTopicBuffer(cap)
		return
	}
	// Already buffering. Resize only if cap differs to avoid
	// dropping queued events on a no-op re-enable.
	if existing.cap == cap {
		return
	}
	existing.mu.Lock()
	defer existing.mu.Unlock()
	existing.cap = cap
	if len(existing.events) > cap {
		// Keep the most recent `cap` events; older ones evict.
		existing.events = append([]Event(nil), existing.events[len(existing.events)-cap:]...)
	}
}

// ClearBuffer removes the topic's replay buffer, freeing memory
// eagerly. Subsequent Publish calls for `topic` still reach live
// subscribers — they just no longer accumulate in a replay ring.
// Safe to call on a topic that was never buffered.
func (h *Hub) ClearBuffer(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.topicBuffers, topic)
}

// Subscribe registers a new subscriber. topics may be empty to
// receive every event regardless of topic; otherwise only events
// whose Topic matches one of the registered names are delivered.
//
// Returns a receive-only channel and a cancel func. The handler
// MUST call cancel when the client disconnects, otherwise the
// subscription leaks.
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

		// Then close the channel under the subscription's own mutex
		// so it serialises with deliverTo. Without this, a Publish
		// that picked up the sub before the delete could race close
		// → panic on send-to-closed-channel.
		s.mu.Lock()
		s.closed = true
		close(s.ch)
		s.mu.Unlock()
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

	// If the topic has a replay buffer, append BEFORE live delivery.
	// Order matters: a late SubscribeWithReplay (one that races
	// Publish) sees the event in either the snapshot (if we appended
	// before they snapshotted) OR the live channel (if we appended
	// after) — never both, because subscribe holds h.mu.Lock for the
	// add+snapshot and we hold h.mu.RLock here.
	if buf, ok := h.topicBuffers[e.Topic]; ok {
		buf.append(e)
	}

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

// SubscribeWithReplay is the variant the per-instance SSE handler
// uses. Under a single hub lock it:
//
//  1. Snapshots each requested topic's replay buffer
//  2. Pre-fills the subscription's channel with the snapshot
//  3. Registers the subscription so live events start flowing
//
// The pre-fill happens BEFORE the subscription enters h.subs, so
// no publisher can interleave a live event into the channel during
// the drain. Ordering guarantee: replay events appear in
// publish-order, immediately followed by any live events published
// after Subscribe returns.
//
// The channel is sized max(h.bufLen, total replay events) so the
// pre-fill can never block.
//
// Returns the channel + cancel func, same as Subscribe.
func (h *Hub) SubscribeWithReplay(topics ...string) (<-chan Event, func()) {
	// Everything happens under h.mu.Lock: Publish holds h.mu.RLock,
	// so no event can land in a topic buffer between our snapshot
	// and the subscription becoming visible — each event ends up in
	// either the snapshot or the live channel, never both or neither.
	h.mu.Lock()
	defer h.mu.Unlock()

	totalReplay := 0
	snapshots := make(map[string][]Event, len(topics))
	for _, t := range topics {
		if buf, ok := h.topicBuffers[t]; ok {
			snap := buf.snapshot()
			snapshots[t] = snap
			totalReplay += len(snap)
		}
	}
	chanSize := h.bufLen
	if totalReplay > chanSize {
		chanSize = totalReplay
	}

	s := &subscription{
		topics: make(map[string]struct{}, len(topics)),
		ch:     make(chan Event, chanSize),
	}
	for _, t := range topics {
		s.topics[t] = struct{}{}
	}

	// Pre-fill BEFORE registering. No publisher can send to s.ch
	// yet because s isn't in h.subs. Iterate topics in the order
	// the caller gave them so cross-topic replay is deterministic.
	for _, t := range topics {
		for _, ev := range snapshots[t] {
			s.ch <- ev // never blocks: chanSize ≥ totalReplay
		}
	}

	// NOW make the subscription visible to publishers.
	h.subs[s] = struct{}{}

	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subs[s]; !ok {
			h.mu.Unlock()
			return
		}
		delete(h.subs, s)
		h.mu.Unlock()
		s.mu.Lock()
		s.closed = true
		close(s.ch)
		s.mu.Unlock()
	}
	return s.ch, cancel
}

// deliverTo handles the drop-oldest semantics for one subscriber.
// Tries a non-blocking send; if the buffer is full, drains the
// oldest event, increments droppedSinceWarn, and retries.
//
// Holds s.mu for the entire duration: it serialises with the cancel
// path (no close(s.ch) between our fullness check and the send) and
// with other publishers (two concurrent drains on the same channel
// would double-count drops). If the subscription is already closed,
// the function is a no-op.
//
// Drop accounting uses Swap(0): a Load + Store pair would be racy — a
// concurrent Publish could increment droppedSinceWarn between the Load
// and Store, losing the increment.
func (h *Hub) deliverTo(s *subscription, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	// Prepend the dropped-warning event if we owe one. The payload
	// is just the count, for the client to decide whether to
	// refetch. Try non-blocking; if it doesn't fit either, restore
	// the count and let the next call handle it.
	if lost := s.droppedSinceWarn.Swap(0); lost > 0 {
		select {
		case s.ch <- Event{SchemaVersion: EventSchemaVersion,
			Topic: "dropped", Data: []byte(strconv.FormatUint(lost, 10))}:
		default:
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

// Close shuts down the hub. Every active subscriber is closed,
// the hub's set is cleared, and subsequent Publish calls become
// no-ops returning 0. Idempotent — calling Close twice is safe.
//
// The server's shutdown path calls this so in-flight EventSource
// connections disconnect immediately instead of leaking until each
// connection dies on its own.
func (h *Hub) Close() {
	h.mu.Lock()
	subs := make([]*subscription, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.subs = make(map[*subscription]struct{}) // clear; Publish becomes no-op
	h.mu.Unlock()
	for _, s := range subs {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
		s.mu.Unlock()
	}
}

// WaitForSubscribers polls Stats().Subscribers until it reaches
// n or ctx expires, returning true when the count is reached.
// Tests use this instead of a flaky time.Sleep to deterministically
// wait for an SSE handler to register its subscription.
func (h *Hub) WaitForSubscribers(ctx context.Context, n int) bool {
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if h.Stats().Subscribers >= n {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}
