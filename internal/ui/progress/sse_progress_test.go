package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TestSSEProgress_PublishesTypedStepEvents pins the JSON wire
// shape the frontend consumes. webui-create.jsx's event log
// renderer switches on `kind`; a missing or renamed field would
// silently break the modal even though the SSE stream still
// "works."
func TestSSEProgress_PublishesTypedStepEvents(t *testing.T) {
	hub := stream.New()
	defer hub.Close()
	hub.EnableBuffering(TopicFor("demo"), 32)

	p := New(hub, "demo")
	p.StartStep(localnet.StepPreflight, "")
	p.UpdateStep(localnet.StepStartServices, "11/15 containers up", 71)
	p.FinishStep(localnet.StepStartServices, "")
	p.FailStep(localnet.StepWaitHealthy, "Timed out", errors.New("ctx done"))
	p.Warn("dev secret in use")
	p.Done("LocalNet ready")

	ch, cancel := hub.SubscribeWithReplay(TopicFor("demo"))
	defer cancel()
	events := drain(t, ch, 6, time.Second)

	type got struct {
		kind, step, detail, summary, cause, message string
		percent                                     int
	}
	parsed := make([]got, len(events))
	for i, ev := range events {
		var raw map[string]any
		if err := json.Unmarshal(ev.Data, &raw); err != nil {
			t.Fatalf("event %d JSON decode: %v", i, err)
		}
		parsed[i] = got{
			kind:    str(raw["kind"]),
			step:    str(raw["step"]),
			detail:  str(raw["detail"]),
			summary: str(raw["summary"]),
			cause:   str(raw["cause"]),
			message: str(raw["message"]),
			percent: intOf(raw["percent"]),
		}
	}

	want := []got{
		{kind: "step.started", step: "preflight"},
		{kind: "step.progress", step: "start_services", detail: "11/15 containers up", percent: 71},
		{kind: "step.finished", step: "start_services"},
		{kind: "step.failed", step: "wait_healthy", summary: "Timed out", cause: "ctx done"},
		{kind: "warn", message: "dev secret in use"},
		{kind: "done", detail: "LocalNet ready"},
	}
	for i, w := range want {
		if parsed[i] != w {
			t.Errorf("event %d:\n  got  %+v\n  want %+v", i, parsed[i], w)
		}
	}
}

// TestSSEProgress_IDsMonotonic — the frontend's gap-detection
// relies on event IDs being strictly increasing per stream. A
// concurrent emit must not skip or reorder.
func TestSSEProgress_IDsMonotonic(t *testing.T) {
	hub := stream.New()
	defer hub.Close()
	hub.EnableBuffering(TopicFor("demo"), 64)

	p := New(hub, "demo")
	for i := 0; i < 20; i++ {
		p.Warn(fmt.Sprintf("msg%d", i))
	}

	ch, cancel := hub.SubscribeWithReplay(TopicFor("demo"))
	defer cancel()
	events := drain(t, ch, 20, time.Second)

	// IDs are stringified uint64s; compare numerically, not
	// lexicographically (otherwise "9" > "10" trips the check).
	for i := 0; i < len(events)-1; i++ {
		gotN, _ := strconv.ParseUint(events[i].ID, 10, 64)
		nextN, _ := strconv.ParseUint(events[i+1].ID, 10, 64)
		if gotN >= nextN {
			t.Errorf("event %d ID=%q not before %d ID=%q",
				i, events[i].ID, i+1, events[i+1].ID)
		}
	}
	if events[0].ID != "1" {
		t.Errorf("first event ID = %q, want %q (monotonic from 1)", events[0].ID, "1")
	}
}

// TestSSEProgress_OutErrEmitOutputEvents pins the io.Writer
// adapter contract: each Write produces ONE event with stream
// = "stdout" or "stderr", and the text drops the trailing
// newline so the SSE wire layer doesn't emit a trailing empty
// data: line.
func TestSSEProgress_OutErrEmitOutputEvents(t *testing.T) {
	hub := stream.New()
	defer hub.Close()
	hub.EnableBuffering(TopicFor("demo"), 32)

	p := New(hub, "demo")
	if _, err := p.Out().Write([]byte("ledger ready\n")); err != nil {
		t.Fatalf("Out write: %v", err)
	}
	if _, err := p.Err().Write([]byte("warning: dev secret\n")); err != nil {
		t.Fatalf("Err write: %v", err)
	}

	ch, cancel := hub.SubscribeWithReplay(TopicFor("demo"))
	defer cancel()
	events := drain(t, ch, 2, time.Second)

	var stdout, stderr outputPayload
	for _, ev := range events {
		var pl outputPayload
		if err := json.Unmarshal(ev.Data, &pl); err != nil {
			t.Fatalf("decode: %v", err)
		}
		switch pl.Stream {
		case "stdout":
			stdout = pl
		case "stderr":
			stderr = pl
		}
	}
	if stdout.Kind != "output" || stdout.Text != "ledger ready" {
		t.Errorf("stdout event = %+v, want kind=output text=\"ledger ready\"", stdout)
	}
	if stderr.Kind != "output" || stderr.Text != "warning: dev secret" {
		t.Errorf("stderr event = %+v, want kind=output text=\"warning: dev secret\"", stderr)
	}
}

// TestSSEProgress_OutWriter_EmptyAndWhitespaceWritesSkipped —
// `fmt.Fprintln(w)` (just "\n") is common in the localnet code
// for spacing; we don't want every blank line to fire an empty
// output event on the wire. Pin the no-emit contract.
func TestSSEProgress_OutWriter_EmptyAndWhitespaceWritesSkipped(t *testing.T) {
	hub := stream.New()
	defer hub.Close()
	hub.EnableBuffering(TopicFor("demo"), 8)

	p := New(hub, "demo")
	_, _ = p.Out().Write([]byte(""))
	_, _ = p.Out().Write([]byte("\n"))
	_, _ = p.Out().Write([]byte("\n\n"))
	// Then a real write — only this should produce an event.
	_, _ = p.Out().Write([]byte("real content\n"))

	ch, cancel := hub.SubscribeWithReplay(TopicFor("demo"))
	defer cancel()
	events := drain(t, ch, 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	var pl outputPayload
	if err := json.Unmarshal(events[0].Data, &pl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pl.Text != "real content" {
		t.Errorf("text=%q, want %q", pl.Text, "real content")
	}
}

// TestSSEProgress_TopicNamespaced — events for instance "alpha"
// must never appear on the "beta" subscription. Pins the
// per-instance isolation that the create-flow relies on.
func TestSSEProgress_TopicNamespaced(t *testing.T) {
	hub := stream.New()
	defer hub.Close()
	hub.EnableBuffering(TopicFor("alpha"), 8)
	hub.EnableBuffering(TopicFor("beta"), 8)

	pa := New(hub, "alpha")
	pb := New(hub, "beta")
	pa.Warn("alpha-msg")
	pb.Warn("beta-msg")

	chA, cancelA := hub.SubscribeWithReplay(TopicFor("alpha"))
	defer cancelA()
	chB, cancelB := hub.SubscribeWithReplay(TopicFor("beta"))
	defer cancelB()

	evA := drain(t, chA, 1, time.Second)
	evB := drain(t, chB, 1, time.Second)

	if !strings.Contains(string(evA[0].Data), "alpha-msg") {
		t.Errorf("alpha subscriber got non-alpha event: %s", evA[0].Data)
	}
	if !strings.Contains(string(evB[0].Data), "beta-msg") {
		t.Errorf("beta subscriber got non-beta event: %s", evB[0].Data)
	}
}

// TestSSEProgress_SatisfiesProgressInterface is a compile-time
// proof that SSEProgress is a valid localnet.Progress impl.
func TestSSEProgress_SatisfiesProgressInterface(t *testing.T) {
	var _ localnet.Progress = (*SSEProgress)(nil)
}

func drain(t *testing.T, ch <-chan stream.Event, n int, timeout time.Duration) []stream.Event {
	t.Helper()
	out := make([]stream.Event, 0, n)
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed at %d/%d events", len(out), n)
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timeout at %d/%d events", len(out), n)
		}
	}
	return out
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intOf(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
