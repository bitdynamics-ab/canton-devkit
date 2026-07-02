// Package progress provides the SSE-backed implementation of
// internal/localnet.Progress. It lives in its own package so the
// dependency direction stays one-way: localnet must not import the
// UI's stream hub, and stream stays generic pub/sub with no
// localnet awareness.
package progress

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"sync/atomic"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TopicFor returns the SSE topic (`instance:<name>`) an instance's
// progress events are published on. Kept as a function so publisher
// and subscriber can never drift on the format.
func TopicFor(name string) string {
	return "instance:" + name
}

// SSEProgress implements localnet.Progress by publishing typed
// JSON events to a per-instance topic on a *stream.Hub — one per
// in-flight POST /api/instances goroutine.
//
// Event ID is a monotonic counter so the browser's EventSource can
// detect gaps in the stream.
//
// Wire shape per event Data field — a JSON object discriminated by
// `kind`, which the frontend's CreateProgressModal switches on:
//
//	{"kind":"step.started",  "step":"preflight"}
//	{"kind":"step.progress", "step":"start_services", "detail":"11/15", "percent":71}
//	{"kind":"step.finished", "step":"preflight"}
//	{"kind":"step.failed",   "step":"start_services", "summary":"…", "cause":"…"}
//	{"kind":"warn",          "message":"using uncurated …"}
//	{"kind":"done",          "detail":"Canton LocalNet \"demo\" is ready."}
//	{"kind":"output",        "stream":"stdout"|"stderr", "text":"…"}
type SSEProgress struct {
	hub   *stream.Hub
	topic string

	// seq is the monotonic event counter; atomic so concurrent
	// emitters don't lose increments.
	seq atomic.Uint64

	// Out/Err writers are constructed once and cached. Tests
	// rely on stable pointer identity across calls.
	out *eventWriter
	err *eventWriter
}

// New constructs an SSEProgress for the named instance. Caller is
// responsible for hub.EnableBuffering(TopicFor(name), …) BEFORE
// construction so the buffer captures the first event.
func New(hub *stream.Hub, name string) *SSEProgress {
	p := &SSEProgress{
		hub:   hub,
		topic: TopicFor(name),
	}
	p.out = &eventWriter{p: p, stream: "stdout"}
	p.err = &eventWriter{p: p, stream: "stderr"}
	return p
}

// publish encodes the payload as JSON and ships it on the hub.
// Marshal errors are dropped: encoding our own typed structs cannot
// fail, and a progress-publishing failure must never mask the real
// error from RunUp.
func (p *SSEProgress) publish(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	p.hub.Publish(stream.Event{
		SchemaVersion: stream.EventSchemaVersion,
		Topic:         p.topic,
		ID:            strconv.FormatUint(p.seq.Add(1), 10),
		Data:          data,
	})
}

// stepPayload is the common shape for step.started/finished/failed.
type stepPayload struct {
	Kind    string `json:"kind"`
	Step    string `json:"step"`
	Detail  string `json:"detail,omitempty"`
	Percent int    `json:"percent,omitempty"`
	Summary string `json:"summary,omitempty"`
	Cause   string `json:"cause,omitempty"`
	// ErrorCode is a stable machine-readable code from
	// internal/localnet.CodedError, set on step.failed when RunUp
	// recognized the failure mode (PORTS_IN_USE, DOCKER_DOWN, …).
	// The frontend switches on it to render specific remediation
	// panels instead of generic error text.
	ErrorCode string `json:"error_code,omitempty"`
}

type warnPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type donePayload struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

type outputPayload struct {
	Kind   string `json:"kind"`
	Stream string `json:"stream"` // "stdout" or "stderr"
	Text   string `json:"text"`
}

// StartStep publishes a "step.started" event.
func (p *SSEProgress) StartStep(step localnet.Step, detail string) {
	p.publish(stepPayload{Kind: "step.started", Step: string(step), Detail: detail})
}

// UpdateStep publishes a "step.progress" event with optional
// percent, used for the mid-step progress bar and sub-text.
func (p *SSEProgress) UpdateStep(step localnet.Step, detail string, percent int) {
	p.publish(stepPayload{
		Kind: "step.progress", Step: string(step), Detail: detail, Percent: percent,
	})
}

// FinishStep publishes a "step.finished" event.
func (p *SSEProgress) FinishStep(step localnet.Step, detail string) {
	p.publish(stepPayload{Kind: "step.finished", Step: string(step), Detail: detail})
}

// FailStep publishes a "step.failed" event. cause is stringified so
// the wire stays JSON-safe; nil cause omits the field. When cause
// carries a CodedError (via localnet.WithCode), the machine-readable
// code is stamped on the payload.
func (p *SSEProgress) FailStep(step localnet.Step, summary string, cause error) {
	pl := stepPayload{Kind: "step.failed", Step: string(step), Summary: summary}
	if cause != nil {
		pl.Cause = cause.Error()
		pl.ErrorCode = localnet.CodeOf(cause)
	}
	p.publish(pl)
}

// Warn publishes a warning event.
func (p *SSEProgress) Warn(message string) {
	p.publish(warnPayload{Kind: "warn", Message: message})
}

// Done publishes the terminal success marker. The handler-side
// goroutine should call hub.ClearBuffer immediately after to free
// the replay ring.
func (p *SSEProgress) Done(detail string) {
	p.publish(donePayload{Kind: "done", Detail: detail})
}

// Out returns the writer for verbatim stdout-style text. Each Write
// call produces one "output" event with stream="stdout"; text is
// not line-buffered, so callers writing line-terminated chunks see
// them shipped immediately.
func (p *SSEProgress) Out() io.Writer { return p.out }

// Err returns the writer for stderr-style text — same shape as Out
// with stream="stderr" so the frontend can color the two streams
// differently.
func (p *SSEProgress) Err() io.Writer { return p.err }

// eventWriter adapts io.Writer to an "output" event publisher.
// Each Write produces one event; empty writes yield none.
type eventWriter struct {
	p      *SSEProgress
	stream string
}

func (w *eventWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	// Strip a trailing newline so the SSE wire path's split-on-
	// newline doesn't emit a trailing empty data line. A pure-
	// newline payload (e.g. fmt.Fprintln(w) spacing) is skipped
	// entirely rather than emitted as an empty event.
	text := bytes.TrimRight(b, "\n")
	if len(text) == 0 {
		return len(b), nil
	}
	w.p.publish(outputPayload{
		Kind:   "output",
		Stream: w.stream,
		Text:   string(text),
	})
	return len(b), nil
}

// PublishCancelled emits a synthetic kind=cancelled marker on a
// per-instance topic. The DELETE /api/instances/{name}/up handler
// calls it BEFORE cancelling the in-flight goroutine's context, so
// the SSE consumer sees the user-initiated cancellation ahead of
// the step.failed events that follow when RunUp notices ctx.Err(),
// and can render "cancelled" UX instead of a generic failure.
//
// reason is surfaced verbatim. No event ID — it's a one-shot
// synthetic that consumers don't gap-detect on; the hub still
// buffers it if the topic has buffering enabled.
func PublishCancelled(hub *stream.Hub, name, reason string) {
	payload, _ := json.Marshal(struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason,omitempty"`
	}{Kind: "cancelled", Reason: reason})
	hub.Publish(stream.Event{
		SchemaVersion: stream.EventSchemaVersion,
		Topic:         TopicFor(name),
		Data:          payload,
	})
}
