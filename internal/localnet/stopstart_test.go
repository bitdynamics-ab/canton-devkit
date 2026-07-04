package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// --- RunStop --------------------------------------------------------------

type stopperFn func(context.Context) error

func (f stopperFn) StopContainers(ctx context.Context) error { return f(ctx) }

func fakeStopper(fn func(context.Context) error) func(string, string, io.Writer) composeStopper {
	return func(string, string, io.Writer) composeStopper { return stopperFn(fn) }
}

func TestRunStop_StopsRunningInstance(t *testing.T) {
	seedRunningInstance(t, "demo")
	called := false
	opts := &StopOptions{
		Name:      "demo",
		NewRunner: fakeStopper(func(context.Context) error { called = true; return nil }),
	}
	var out, errb bytes.Buffer
	if exit := RunStop(context.Background(), &out, &errb, opts); exit != ExitSuccess {
		t.Fatalf("RunStop exit=%d, stderr=%s", exit, errb.String())
	}
	if !called {
		t.Error("StopContainers was not invoked")
	}
	if s, _ := registry.Read("demo"); s.Status != registry.StatusStopped {
		t.Errorf("status = %s, want stopped", s.Status)
	}
}

func TestRunStop_AlreadyStopped(t *testing.T) {
	seedRunningInstance(t, "demo")
	// Flip to stopped first.
	s, _ := registry.Read("demo")
	s.Status = registry.StatusStopped
	_ = registry.Write(s)

	opts := &StopOptions{
		Name:      "demo",
		NewRunner: fakeStopper(func(context.Context) error { t.Fatal("must not call compose"); return nil }),
	}
	var out, errb bytes.Buffer
	if exit := RunStop(context.Background(), &out, &errb, opts); exit != ExitSuccess {
		t.Fatalf("stopping an already-stopped instance should be a no-op success, got %d", exit)
	}
}

func TestRunStop_UnknownInstance(t *testing.T) {
	seedRunningInstance(t, "demo") // sets registry root; "ghost" not seeded
	opts := &StopOptions{Name: "ghost"}
	var out, errb bytes.Buffer
	if exit := RunStop(context.Background(), &out, &errb, opts); exit != ExitUserError {
		t.Fatalf("unknown instance should be ExitUserError, got %d", exit)
	}
}

func TestRunStop_ComposeFailureMarksFailed(t *testing.T) {
	seedRunningInstance(t, "demo")
	opts := &StopOptions{
		Name:      "demo",
		NewRunner: fakeStopper(func(context.Context) error { return errors.New("boom") }),
	}
	var out, errb bytes.Buffer
	if exit := RunStop(context.Background(), &out, &errb, opts); exit != ExitRuntimeFailure {
		t.Fatalf("compose failure should be ExitRuntimeFailure, got %d", exit)
	}
	if s, _ := registry.Read("demo"); s.Status != registry.StatusFailed {
		t.Errorf("status = %s, want failed", s.Status)
	}
}

// --- RunStart -------------------------------------------------------------

type starterFn struct {
	start func(context.Context) error
	wait  func(context.Context) error
}

func (s starterFn) Start(ctx context.Context) error          { return s.start(ctx) }
func (s starterFn) WaitForHealthy(ctx context.Context) error { return s.wait(ctx) }

func fakeStarter(s starterFn) func(string, string, io.Writer) composeStarter {
	return func(string, string, io.Writer) composeStarter { return s }
}

func TestRunStart_FastStartStoppedWithContainers(t *testing.T) {
	seedRunningInstance(t, "demo")
	s, _ := registry.Read("demo")
	s.Status = registry.StatusStopped
	_ = registry.Write(s)

	started := false
	opts := &StartOptions{
		Name:     "demo",
		SkipWait: true,
		listContainers: func(context.Context, string) ([]containers.Info, error) {
			return []containers.Info{{Service: "canton", State: "exited"}}, nil
		},
		NewRunner: fakeStarter(starterFn{
			start: func(context.Context) error { started = true; return nil },
			wait:  func(context.Context) error { return nil },
		}),
		runUp: func(context.Context, Progress, *UpOptions) int {
			t.Fatal("must not fall back to up when containers exist")
			return ExitRuntimeFailure
		},
	}
	var out, errb bytes.Buffer
	if exit := RunStart(context.Background(), NewTextProgress(&out, &errb), &out, &errb, opts); exit != ExitSuccess {
		t.Fatalf("RunStart exit=%d, stderr=%s", exit, errb.String())
	}
	if !started {
		t.Error("Start was not invoked")
	}
	if s, _ := registry.Read("demo"); s.Status != registry.StatusRunning {
		t.Errorf("status = %s, want running", s.Status)
	}
}

func TestRunStart_FallsBackToUpWhenContainersGone(t *testing.T) {
	seedRunningInstance(t, "demo")
	s, _ := registry.Read("demo")
	s.Status = registry.StatusStopped
	_ = registry.Write(s)

	upCalled := false
	opts := &StartOptions{
		Name: "demo",
		listContainers: func(context.Context, string) ([]containers.Info, error) {
			return nil, nil // no containers
		},
		runUp: func(_ context.Context, _ Progress, o *UpOptions) int {
			upCalled = true
			if o.Name != "demo" {
				t.Errorf("up name = %q, want demo", o.Name)
			}
			if o.Version != "0.6.4" {
				t.Errorf("up reused version = %q, want 0.6.4", o.Version)
			}
			return ExitSuccess
		},
	}
	var out, errb bytes.Buffer
	if exit := RunStart(context.Background(), NewTextProgress(&out, &errb), &out, &errb, opts); exit != ExitSuccess {
		t.Fatalf("RunStart exit=%d, stderr=%s", exit, errb.String())
	}
	if !upCalled {
		t.Error("expected fallback to RunUp when containers were removed")
	}
}

func TestRunStart_UnregisteredRunsUp(t *testing.T) {
	seedRunningInstance(t, "demo") // sets registry root; "fresh" not seeded
	upCalled := false
	opts := &StartOptions{
		Name: "fresh",
		runUp: func(_ context.Context, _ Progress, o *UpOptions) int {
			upCalled = true
			if o.Name != "fresh" {
				t.Errorf("up name = %q, want fresh", o.Name)
			}
			return ExitSuccess
		},
	}
	var out, errb bytes.Buffer
	if exit := RunStart(context.Background(), NewTextProgress(&out, &errb), &out, &errb, opts); exit != ExitSuccess {
		t.Fatalf("RunStart exit=%d, stderr=%s", exit, errb.String())
	}
	if !upCalled {
		t.Error("expected RunUp for an unregistered instance")
	}
}

func TestRunStart_AlreadyRunning(t *testing.T) {
	seedRunningInstance(t, "demo")
	opts := &StartOptions{
		Name:  "demo",
		runUp: func(context.Context, Progress, *UpOptions) int { t.Fatal("must not run up"); return 0 },
	}
	var out, errb bytes.Buffer
	if exit := RunStart(context.Background(), NewTextProgress(&out, &errb), &out, &errb, opts); exit != ExitSuccess {
		t.Fatalf("start of a running instance should be a no-op success, got %d", exit)
	}
}

func TestRunStart_PausedIsUserError(t *testing.T) {
	seedRunningInstance(t, "demo")
	s, _ := registry.Read("demo")
	s.Status = registry.StatusPaused
	_ = registry.Write(s)

	opts := &StartOptions{Name: "demo"}
	var out, errb bytes.Buffer
	if exit := RunStart(context.Background(), NewTextProgress(&out, &errb), &out, &errb, opts); exit != ExitUserError {
		t.Fatalf("start of a paused instance should be ExitUserError, got %d", exit)
	}
}
