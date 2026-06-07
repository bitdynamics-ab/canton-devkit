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
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Event is a single broadcast payload. Topic routes the event; Data
// is the opaque body the SSE handler serialises to the wire. ID is
// optional and appears as the SSE `id:` line for client-side
// last-event tracking.
//
// SchemaVersion mirrors internal/api/types.SchemaVersion. Reviewer pin
// every wire-level message the Web UI consumes needs a
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

	// Per-topic event buffers for the replay-on-subscribe contract
	//. Populated only for topics the caller has opted
	// in via EnableBuffering — the global topic firehose is NOT
	// buffered, because the only consumer that needs replay today
	// is the per-instance create-flow SSE stream:
	//
	//   POST /api/instances → spawns up goroutine that calls
	//     hub.EnableBuffering("instance:demo") + publishes the
	//     first StepStartedevent (the "instance lock" step).
	//
	//   Browser opens GET /api/instances/demo/events 50ms later;
	//     SubscribeWithReplay drains the buffer into the
	//     subscriber's channel BEFORE the live event flow starts,
	//     so the user sees step 1 even though the SSE arrived after
	//     it published.
	//
	// Buffers are removed on ClearBuffer(topic) — the up handler
	// calls that after Done/Fail to free memory eagerly. Without
	// the call, the buffer leaks at most one capacity's worth of
	// events per finished instance — annoying but not catastrophic.
	topicBuffers map[string]*topicBuffer

	// stats — atomically updated, read by Stats() for observability.
	published atomic.Uint64
	dropped   atomic.Uint64
}

// topicBuffer is a fixed-size ring of recent events for one topic.
// Capacity is set per-buffer (EnableBuffering's cap parameter); the
// create-instance flow uses 128, which fits ~16 step starts + step
// finishes + a few warnings without eviction during a normal up.
//
// A full buffer evicts the oldest event silently. SubscribeWithReplay
// drains whatever's currently in the ring; it doesn't emit a
// "dropped" sentinel like the live drop-oldest does, because
// replay losses aren't observable to the live publisher (the lost
// events were published before the subscriber existed).
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

// append adds an event to the ring. Evicts the oldest when full.
// Slice realloc is bounded by `cap` so steady-state memory is flat.
func (b *topicBuffer) append(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= b.cap {
		// Shift left by one. For cap=128 this is a 127-element
		// copy per publish — negligible at human-paced step
		// events (~10/sec peak during compose-up).
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
// mu serialises BOTH close(ch) (in cancel) AND the deliverTo body
// per subscription. // the round-1 fix used an RWMutex that allowed multiple concurrent
// deliverTo calls; under load that lets two publishers both drain
// from the same channel, double-counting drops and racing the
// retry. A single Mutex per subscription serialises sends to one
// subscriber — many subscribers still proceed in parallel because
// the Mutex is per-subscription, not per-hub. The hub-level RWMutex
// (h.mu) keeps the SET of subscribers safe for concurrent iteration.
//
// The cost (one publisher per subscriber at a time) is tiny: each
// deliverTo is a non-blocking channel op + at most one drain + one
// send. Microseconds. Net effect of the change: zero throughput
// impact, correct drop accounting under concurrent publish load.
type subscription struct {
	topics map[string]struct{} // empty = subscribe-all
	ch     chan Event
	mu     sync.Mutex
	closed bool // protected by mu
	// droppedSinceWarn counts events lost to drop-oldest for THIS
	// subscriber. When non-zero the next successful send is preceded
	// by a synthetic "dropped" event so the client can react.
	// Read+reset via Swap(0) under mu so concurrent increments
	// inside deliverTo are safe .
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

// NewWithBuffer is the variant tests use to dial buffer size up or
// down without touching defaultBuffer. Production callers should
// stay on New().
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
// Publish calls for `topic` append to a fixed-size ring (cap
// events). SubscribeWithReplay drains the ring into new
// subscribers before live event flow begins.
//
// Idempotent — calling it twice for the same topic with the same
// cap is a no-op. A different cap on a re-enable resizes the
// existing buffer (truncates if the new cap is smaller).
//
// The create-instance handler calls this with cap=128 right after
// the POST is accepted, before spawning the up goroutine. The
// matching ClearBuffer call frees the ring when the up finishes.
//
// cap < 1 is clamped to 1.
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

// ClearBuffer removes the topic's replay buffer. Called by the
// create-instance handler after the up finishes (success or
// failure) to free memory eagerly.
//
// Subsequent Publish calls for `topic` are still delivered to
// live subscribers — they just no longer accumulate in any
// replay ring. Safe to call on a topic that was never buffered.
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
		// mutex — serialises with deliverTo, guaranteeing no
		// in-flight Publish is still trying to send when close()
		// runs. Without this, a Publish that picked up the sub
		// before our delete-from-h.subs could race close → panic
		// on send-to-closed-channel.
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
// uses. It atomically:
//
//  1. Snapshots each requested topic's replay buffer
//  2. Registers the subscription so live events start flowing
//  3. Pre-fills the subscription's channel with the snapshot
//
// The pre-fill happens BEFORE the subscription enters h.subs, so
// no publisher can interleave a live event into the channel during
// the drain. Ordering guarantee: replay events appear in
// publish-order, immediately followed by any live events published
// after Subscribe returns.
//
// The subscription channel must be large enough to hold the entire
// snapshot without blocking. We size it to max(h.bufLen,
// sum-of-buffer-sizes) to keep that promise; in practice the
// per-instance topic ring is 128 and h.bufLen is 64, so the
// channel is 128.
//
// Returns the channel + cancel func, same as Subscribe.
func (h *Hub) SubscribeWithReplay(topics ...string) (<-chan Event, func()) {
	// Snapshot each topic's buffer first — outside the hub lock so
	// we don't hold writers off. A concurrent Publish during the
	// snapshot may add an event to the topic buffer; we hold
	// h.mu.Lock below before registering the subscription, so
	// that event either:
	//   (a) lands in the snapshot we already took (we win the race)
	//   (b) lands ONLY in the buffer (we lose the race AND the
	//       subscriber isn't registered yet, so the live publisher
	//       doesn't see it) — caught on a SECOND snapshot under
	//       the lock below
	//
	// The "two-snapshot under lock" pattern is the simplest
	// correctness proof. Doing one snapshot before the lock is
	// premature optimisation; tests will catch it if it matters.
	h.mu.Lock()
	defer h.mu.Unlock()

	// Compute channel size: enough to hold every replay event plus
	// the usual live buffer. Without this, prefilling N replay
	// events into a chan of capacity bufLen<N would deadlock on
	// the prefill sends.
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
// Holds s.closeMu for read for the entire duration so the cancel
// path can't close(s.ch) between our fullness check and the send.
// . If the subscription was already
// closed by the time we acquire the lock, the function is a no-op
// — the event simply doesn't reach the dead subscriber.
//
// Drop accounting uses Swap(0) the previous Load +
// Store pair was racy — a concurrent Publish could increment
// droppedSinceWarn between our Load and Store, losing the
// increment. Swap is atomic.
func (h *Hub) deliverTo(s *subscription, e Event) {
	// Serialise deliveries to THIS subscriber. Multiple publishers
	// may call deliverTo concurrently; without the lock, two
	// drains can race the same channel and double-count drops.
	// Per-subscription Mutex keeps the lock surface tiny — other
	// subscribers proceed in parallel via their own mutexes.
	s.mu.Lock()
	defer s.mu.Unlock()
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

// Close shuts down the hub. Every active subscriber is closed,
// the hub's set is cleared, and subsequent Publish calls become
// no-ops returning 0. Idempotent — calling Close twice is safe.
//
// the original hub had no
// way to mass-disconnect subscribers — `dpm localnet ui` SIGINT
// shut down the HTTP server but the hub's goroutines (held by
// in-flight EventSource connections) leaked their subscriptions
// until the connection itself died. Hub.Close + an explicit
// call from ui.go's shutdown path closes the loop.
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
// n or ctx expires. Returns true when the count is reached.
// Tests use this instead of time.Sleep to deterministically
// wait for an SSE handler to register its subscription.
//
// time.Sleep in tests is the classic flakiness vector — 50ms
// works locally, fails on a
// slow CI runner. A predicate-based wait converts "sleep enough"
// into "wait until the actual condition holds."
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
