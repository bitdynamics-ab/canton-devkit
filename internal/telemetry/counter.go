package telemetry

import "sync"

// Sink accumulates counters during a process run. The CLI holds one (an
// interface, so tests inject a no-op) and deep code increments via the
// package-level Inc, which routes to the installed sink.
type Sink interface {
	Inc(chart, bucket string)
	Persist() // merge the run's counters into the weekly file (+ maybe upload)
}

// noopSink is the default — used in tests and whenever telemetry is not
// installed, so a stray Inc never writes or sends anything.
type noopSink struct{}

func (noopSink) Inc(string, string) {}
func (noopSink) Persist()           {}

var (
	mu      sync.Mutex
	current Sink = noopSink{}
)

// Install sets the process-wide sink (main() installs the real one via
// the CLI; tests leave the no-op default). Idempotent.
func Install(s Sink) {
	mu.Lock()
	defer mu.Unlock()
	if s == nil {
		s = noopSink{}
	}
	current = s
}

// Inc records one counter increment against the installed sink. Unknown
// (chart, bucket) pairs are dropped silently — the allow-list is the
// single source of truth and the compile-time test guards call sites.
func Inc(chart, bucket string) {
	if !allowed(chart, bucket) {
		return
	}
	mu.Lock()
	s := current
	mu.Unlock()
	s.Inc(chart, bucket)
}

// Persist flushes the run's accumulated counters to the weekly file (and
// runs the weekly upload window). Call once at process exit.
func Persist() {
	mu.Lock()
	s := current
	mu.Unlock()
	s.Persist()
}

// counterSink is the real, file-backed sink. It accumulates in memory
// during the run (so N Inc calls cost nothing on disk) and writes once on
// Persist by merging into the current week's aggregate.
type counterSink struct {
	mu       sync.Mutex
	channel  string
	counters map[string]map[string]int // chart -> bucket -> count
}

// NewSink builds a file-backed sink for the given release channel. The
// caller (CLI) installs it only when telemetry is enabled.
func NewSink(channel string) Sink {
	return &counterSink{channel: channel, counters: map[string]map[string]int{}}
}

func (c *counterSink) Inc(chart, bucket string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.counters[chart]
	if m == nil {
		m = map[string]int{}
		c.counters[chart] = m
	}
	m[bucket]++
}

func (c *counterSink) Persist() {
	c.mu.Lock()
	snapshot := c.counters
	c.counters = map[string]map[string]int{}
	c.mu.Unlock()
	if len(snapshot) == 0 {
		return
	}
	if debugEnabled() {
		debugDump(currentWeek(), snapshot)
		return // debug mode never writes the spool or sends
	}
	_ = mergeWeekly(snapshot)
	tryWeeklyUpload()
}
