package localnet

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// pauserFn adapts inline functions to composePauser.
type pauserFn struct {
	pause   func(context.Context) error
	unpause func(context.Context) error
}

func (p pauserFn) Pause(ctx context.Context) error   { return p.pause(ctx) }
func (p pauserFn) Unpause(ctx context.Context) error { return p.unpause(ctx) }

func fakePauser(p composePauser) func(string, []string, []string, []string, string, io.Writer) composePauser {
	return func(string, []string, []string, []string, string, io.Writer) composePauser { return p }
}

func TestRunPause_FreezesRunningInstance(t *testing.T) {
	seedRunningInstance(t, "demo")
	called := false
	opts := &PauseOptions{
		Name: "demo",
		NewRunner: fakePauser(pauserFn{
			pause:   func(context.Context) error { called = true; return nil },
			unpause: func(context.Context) error { return nil },
		}),
	}
	var out, errb bytes.Buffer
	if exit := RunPause(context.Background(), &out, &errb, opts); exit != ExitSuccess {
		t.Fatalf("RunPause exit=%d, stderr=%s", exit, errb.String())
	}
	if !called {
		t.Error("Pause was not invoked")
	}
	if s, _ := registry.Read("demo"); s.Status != registry.StatusPaused {
		t.Errorf("status = %s, want paused", s.Status)
	}
}

func TestRunResume_RequiresPaused(t *testing.T) {
	seedRunningInstance(t, "demo") // running, not paused
	opts := &PauseOptions{
		Name: "demo",
		NewRunner: fakePauser(pauserFn{
			pause:   func(context.Context) error { return nil },
			unpause: func(context.Context) error { return nil },
		}),
	}
	var out, errb bytes.Buffer
	if exit := RunResume(context.Background(), &out, &errb, opts); exit != ExitUserError {
		t.Fatalf("resume of a running instance should be a user error, got exit=%d", exit)
	}
}

func TestRunPause_RoundTrip(t *testing.T) {
	seedRunningInstance(t, "demo")
	mk := func() *PauseOptions {
		return &PauseOptions{
			Name: "demo",
			NewRunner: fakePauser(pauserFn{
				pause:   func(context.Context) error { return nil },
				unpause: func(context.Context) error { return nil },
			}),
		}
	}
	var b bytes.Buffer
	if RunPause(context.Background(), &b, &b, mk()) != ExitSuccess {
		t.Fatal("pause failed")
	}
	if RunResume(context.Background(), &b, &b, mk()) != ExitSuccess {
		t.Fatalf("resume failed: %s", b.String())
	}
	if s, _ := registry.Read("demo"); s.Status != registry.StatusRunning {
		t.Errorf("after resume status = %s, want running", s.Status)
	}
}
