package stream

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	SchemaVersion int    `json:"schema_version"`
	Topic         string `json:"topic"`
	ID            string `json:"id,omitempty"`
	Data          []byte `json:"data,omitempty"`
}

const EventSchemaVersion = 1

const defaultBuffer = 64

type Hub struct {
	mu     sync.RWMutex
	subs   map[*subscription]struct{}
	bufLen int

	topicBuffers map[string]*topicBuffer

	published atomic.Uint64
	dropped   atomic.Uint64
}

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

func (b *topicBuffer) append(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= b.cap {
		copy(b.events, b.events[1:])
		b.events = b.events[:len(b.events)-1]
	}
	b.events = append(b.events, e)
}

func (b *topicBuffer) snapshot() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Event, len(b.events))
	copy(out, b.events)
	return out
}

type subscription struct {
	topics           map[string]struct{}
	ch               chan Event
	mu               sync.Mutex
	closed           bool
	droppedSinceWarn atomic.Uint64
}

func New() *Hub {
	return &Hub{
		subs:         make(map[*subscription]struct{}),
		bufLen:       defaultBuffer,
		topicBuffers: make(map[string]*topicBuffer),
	}
}

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
	if existing.cap == cap {
		return
	}
	existing.mu.Lock()
	defer existing.mu.Unlock()
	existing.cap = cap
	if len(existing.events) > cap {
		existing.events = append([]Event(nil), existing.events[len(existing.events)-cap:]...)
	}
}

func (h *Hub) ClearBuffer(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.topicBuffers, topic)
}

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

func (h *Hub) Publish(e Event) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if buf, ok := h.topicBuffers[e.Topic]; ok {
		buf.append(e)
	}

	delivered := 0
	for s := range h.subs {
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

func (h *Hub) SubscribeWithReplay(topics ...string) (<-chan Event, func()) {
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

	for _, t := range topics {
		for _, ev := range snapshots[t] {
			s.ch <- ev
		}
	}

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

func (h *Hub) deliverTo(s *subscription, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	if lost := s.droppedSinceWarn.Swap(0); lost > 0 {
		select {
		case s.ch <- Event{SchemaVersion: EventSchemaVersion,
			Topic: "dropped", Data: []byte(strconv.FormatUint(lost, 10))}:
		default:
			s.droppedSinceWarn.Add(lost)
		}
	}

	for i := 0; i < 2; i++ {
		select {
		case s.ch <- e:
			return
		default:
			select {
			case <-s.ch:
				s.droppedSinceWarn.Add(1)
				h.dropped.Add(1)
			default:
			}
		}
	}
	s.droppedSinceWarn.Add(1)
	h.dropped.Add(1)
}

type Stats struct {
	Subscribers int
	Published   uint64
	Dropped     uint64
}

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

func (h *Hub) Close() {
	h.mu.Lock()
	subs := make([]*subscription, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.subs = make(map[*subscription]struct{})
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
