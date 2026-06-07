// Package progress provides the SSE-backed implementation of
// internal/localnet.Progress. The localnet package defines the
// interface; this package adapts it to the UI's event hub.
//
// Dependency direction is deliberately one-way:
//
//	internal/ui/handlers ──┐
//	                       ├──► internal/localnet  (Progress interface)
//	                       └──► internal/ui/stream (Hub)
//	internal/ui/progress ──┴──► both
//
// Putting SSEProgress in localnet would force internal/localnet to
// import internal/ui/stream — wrong direction: the orchestrator
// shouldn't know about the UI. Putting it in stream would force
// stream to import localnet — also wrong: stream is generic
// pub/sub, not localnet-aware.
package progress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TopicFor returns the SSE topic an instance's progress events are
// published on. Kept as a function (not a string concatenation
// inlined at every callsite) so the format is impossible to drift —
// changing the prefix here changes both the publisher and the
// subscriber in one edit.
//
// Format: `instance:<name>`. The colon is the conventional
// separator the rest of the codebase uses (see hub.go's topic
// filter examples).
func TopicFor(name string) string {
	return "instance:" + name
}

// SSEProgress implements localnet.Progress by publishing typed
// JSON events to a per-instance topic on a *stream.Hub. The
// create-instance flow uses one of these per in-flight
// POST /api/instances goroutine.
//
// Event ID is a monotonic counter so the browser's EventSource
// can detect gaps in the stream — if event 7 follows event 5 with
// no event 6, the SSE client knows it missed one. EventSource
// also forwards the last-seen ID on reconnect (Last-Event-ID
// header); that's a future affordance — today the create flow
// runs to completion in a single connection.
//
// Wire shape per event Data field — a JSON object discriminated
// by `kind`. Mirrors the webui-create.jsx mockup's event log:
//
//	{"kind":"step.started",  "step":"preflight"}
//	{"kind":"step.progress", "step":"start_services", "detail":"11/15", "percent":71}
//	{"kind":"step.finished", "step":"preflight"}
//	{"kind":"step.failed",   "step":"start_services", "summary":"…", "cause":"…"}
//	{"kind":"warn",          "message":"using uncurated …"}
//	{"kind":"done",          "detail":"Canton LocalNet \"demo\" is ready."}
//	{"kind":"output",        "stream":"stdout"|"stderr", "text":"…"}
//
// The frontend's CreateProgressModal switches on `kind` and
// renders the right row. The `output` events feed the live
// console block at the bottom of the modal.
type SSEProgress struct {
	hub   *stream.Hub
	topic string

	// seq is the monotonic event counter. atomic.Uint64 so
	// concurrent callers (Out and Err writers can run on the
	// goroutine that owns RunUp; nothing else publishes to the
	// per-instance topic today, but defensive) don't lose
	// increments.
	seq atomic.Uint64

	// Out/Err writers are constructed once and cached. Tests
	// rely on stable pointer identity across calls.
	out *eventWriter
	err *eventWriter
}

// New constructs an SSEProgress for the named instance. Caller
// is responsible for hub.EnableBuffering(TopicFor(name), …)
// BEFORE construction so the buffer captures the first event —
// otherwise a fast-publisher / slow-subscriber race loses the
// initial events.
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
// Errors are dropped — JSON marshal of these shapes is total
// (no infinite recursion possible), so a returned error would
// only ever indicate a hub panic, and we don't want orchestrator
// failures to mask the real error from RunUp.
func (p *SSEProgress) publish(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		// Encoding our own typed structs should never fail.
		// Log defensively — invisible to clients but visible
		// in server logs for diagnosis.
		_, _ = fmt.Fprintf(io.Discard, "sse_progress: json encode failed: %v", err)
		return
	}
	id := p.seq.Add(1)
	p.hub.Publish(stream.Event{
		SchemaVersion: stream.EventSchemaVersion,
		Topic:         p.topic,
		ID:            uintToString(id),
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
	// internal/localnet.CodedError. Populated on step.failed when
	// RunUp recognized the failure mode (PORTS_IN_USE, DOCKER_DOWN,
	// etc.). Frontend switches on this to render specific
	// remediation panels instead of generic error text — see
	// .
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

// StartStep publishes a "step.started" event. RunUp calls this for
// every step unconditionally; TextProgress filters CLI-side, but
// the SSE side always emits so the browser renders all 8 phases.
func (p *SSEProgress) StartStep(step localnet.Step, detail string) {
	p.publish(stepPayload{Kind: "step.started", Step: string(step), Detail: detail})
}

// UpdateStep publishes a "step.progress" event with optional
// percent. Frontend uses this for the mid-step progress bar +
// the "11/15 containers up" sub-text.
func (p *SSEProgress) UpdateStep(step localnet.Step, detail string, percent int) {
	p.publish(stepPayload{
		Kind: "step.progress", Step: string(step), Detail: detail, Percent: percent,
	})
}

// FinishStep publishes a "step.finished" event.
func (p *SSEProgress) FinishStep(step localnet.Step, detail string) {
	p.publish(stepPayload{Kind: "step.finished", Step: string(step), Detail: detail})
}

// FailStep publishes a "step.failed" event. cause is stringified
// here so the wire stays JSON-safe; nil cause omits the field.
// When cause carries a CodedError (via localnet.WithCode), the
// machine-readable code is stamped on the payload — frontend
// switches on ErrorCode to render specific remediation panels.
func (p *SSEProgress) FailStep(step localnet.Step, summary string, cause error) {
	pl := stepPayload{Kind: "step.failed", Step: string(step), Summary: summary}
	if cause != nil {
		pl.Cause = cause.Error()
		pl.ErrorCode = localnet.CodeOf(cause)
	}
	p.publish(pl)
}

// Warn publishes a warning event. Frontend renders these as
// amber inline notices in the progress modal.
func (p *SSEProgress) Warn(message string) {
	p.publish(warnPayload{Kind: "warn", Message: message})
}

// Done publishes the terminal success marker. The handler-side
// goroutine should call hub.ClearBuffer immediately after to free
// the replay ring.
func (p *SSEProgress) Done(detail string) {
	p.publish(donePayload{Kind: "done", Detail: detail})
}

// Out returns the writer for verbatim stdout-style text (preflight
// report table, compose log forwarding, endpoint listing). Each
// Write call produces one "output" event with stream="stdout".
// Text is NOT line-buffered — RunUp's callers tend to write
// already-line-terminated chunks, and partial-line buffering
// would mean a chunked Write trickles late.
func (p *SSEProgress) Out() io.Writer { return p.out }

// Err returns the writer for stderr-style text. Same shape, with
// stream="stderr". The frontend's console view distinguishes
// streams to render warnings in a different color.
func (p *SSEProgress) Err() io.Writer { return p.err }

// eventWriter adapts io.Writer to an "output" event publisher.
// Each Write produces one event; an empty Write yields zero
// events (we don't emit empty payloads).
//
// Implements io.Writer so callers like docker.ComposeRunner that
// take a LogWriter can shovel arbitrary bytes into it.
type eventWriter struct {
	p      *SSEProgress
	stream string
}

func (w *eventWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	// Strip a trailing newline if present so the SSE wire path's
	// own split-on-newline doesn't emit a trailing empty data
	// line for the common case where the caller writes a
	// newline-terminated chunk.
	text := bytes.TrimRight(b, "\n")
	if len(text) == 0 {
		// Pure-whitespace payload — skip rather than emit an
		// empty event. Otherwise a `fmt.Fprintln(w)` (which
		// writes just "\n") would generate noise on the wire.
		return len(b), nil
	}
	w.p.publish(outputPayload{
		Kind:   "output",
		Stream: w.stream,
		Text:   string(text),
	})
	return len(b), nil
}

// PublishCancelled emits a synthetic cancellation marker on a
// per-instance topic. Called by the DELETE /api/instances/{name}/up
// handler BEFORE invoking the in-flight goroutine's context
// cancel — so the SSE consumer sees the cancellation as a
// user-initiated event ahead of the natural step.failed events
// that follow when RunUp notices ctx.Err().
//
// The frontend uses the kind=cancelled event to switch from "in
// progress" to "cancelled" UX (different banner copy, different
// retry CTA) rather than treating it as a generic failure.
//
// reason is surfaced verbatim. Today the handler passes "user
// requested via DELETE"; future variants could carry "server
// shutdown" or "policy timeout."
//
// No event ID — this is a one-shot synthetic; SSE consumers
// don't gap-detect on it. The hub still buffers it if the topic
// is in EnableBuffering mode, so a late subscriber sees the
// cancellation.
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

// uintToString — strconv.FormatUint without the import. Same
// helper as in internal/ui/stream/buffer_test.go; kept duplicated
// to avoid coupling a test helper into a production package.
func uintToString(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
