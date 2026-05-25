package localnet

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedDownInstance is the test helper for BIT-124's tests; it writes
// a minimal valid state.json via registry.Write so RunDown's
// registry.Read finds something to operate on.
func seedDownInstance(t *testing.T, name string, status registry.Status) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = status
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// installFakeStopper swaps the package-level stopperFn for the test's
// duration. t.Cleanup restores the prior value so subsequent tests
// (and any code that runs after `go test` finishes a t.Run) don't
// see a leaked fake.
func installFakeStopper(t *testing.T, fn func(ctx context.Context, st *registry.State) error) {
	t.Helper()
	prev := stopperFn
	stopperFn = fn
	t.Cleanup(func() { stopperFn = prev })
}

// TestDown_NotFoundIsUserError exercises the "unknown instance" path.
// Important because the registry returns ErrNotFound rather than a
// generic error here, and the user gets a clearer message + exit
// code 1 than "I/O failure".
func TestDown_NotFoundIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, "ghost")
	if code != localnet.ExitUserError {
		t.Fatalf("code = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), `"ghost"`) {
		t.Errorf("stderr should mention the missing instance name, got %q", errBuf.String())
	}
}

// TestDown_AlreadyStoppedIsNoOpSuccess covers the idempotency
// guarantee from the BIT-124 description: scripts that call `down`
// twice should not get a non-zero on the second call.
func TestDown_AlreadyStoppedIsNoOpSuccess(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusStopped)

	// Install a fake stopper that would fail loudly if it ran; the
	// already-stopped path should not call it at all.
	called := false
	installFakeStopper(t, func(context.Context, *registry.State) error {
		called = true
		return errors.New("stopper should not run on already-stopped instance")
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, "demo")
	if code != localnet.ExitSuccess {
		t.Fatalf("code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if called {
		t.Error("stopper was called on already-stopped instance")
	}
	if !strings.Contains(out.String(), "already stopped") {
		t.Errorf("expected 'already stopped' hint, got %q", out.String())
	}
}

// TestDown_HappyPathStopsAndFlipsStatus drives the full success path
// via the fake stopper, then re-reads the registry to confirm the
// status flipped to stopped. This is the contract the Web UI handler
// (P2-03) will rely on for `GET /api/instances/:name` to show the
// right state after a `POST .../down`.
func TestDown_HappyPathStopsAndFlipsStatus(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusRunning)

	calls := 0
	installFakeStopper(t, func(context.Context, *registry.State) error {
		calls++
		return nil
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, "demo")
	if code != localnet.ExitSuccess {
		t.Fatalf("code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if calls != 1 {
		t.Errorf("stopper called %d times, want 1", calls)
	}

	// Registry status must now be "stopped" — this is the
	// observable side-effect callers depend on.
	got, err := registry.Read("demo")
	if err != nil {
		t.Fatalf("re-read state: %v", err)
	}
	if got.Status != registry.StatusStopped {
		t.Errorf("Status = %q, want %q", got.Status, registry.StatusStopped)
	}

	for _, want := range []string{"Draining ledger", "Stopping services", "Detaching networks", "Stopped LocalNet"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q\nfull=%s", want, out.String())
		}
	}
}

// TestDown_StopFailureMarksPartial covers the negative path: when
// docker compose down fails for a real reason (network gone, daemon
// died), we MUST flip the registry to "partial" so a later `status`
// can show "something's wrong here" instead of falsely reporting
// "stopped". Mirrors up.go's markFailed pattern.
func TestDown_StopFailureMarksPartial(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusRunning)

	installFakeStopper(t, func(context.Context, *registry.State) error {
		return errors.New("docker daemon vanished")
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, "demo")
	if code != localnet.ExitRuntimeFailure {
		t.Fatalf("code = %d, want ExitRuntimeFailure; stderr=%q", code, errBuf.String())
	}

	got, err := registry.Read("demo")
	if err != nil {
		t.Fatalf("re-read state: %v", err)
	}
	if got.Status != registry.StatusPartial {
		t.Errorf("Status = %q, want %q", got.Status, registry.StatusPartial)
	}
	if !strings.Contains(errBuf.String(), "Compose down failed") {
		t.Errorf("stderr missing failure label, got %q", errBuf.String())
	}
}

// TestDown_InvalidNameRejected covers the cobra-layer validation
// (delegates to localnet.ValidateName). A name with a slash should
// never reach RunDown — the command must reject at the flag.
func TestDown_InvalidNameRejected(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	cmd := buildDown()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "bad/name"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --name")
	}
}
