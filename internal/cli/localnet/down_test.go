package localnet

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

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

func installFakeStopper(t *testing.T, fn func(ctx context.Context, st *registry.State) error) {
	t.Helper()
	installStopper(t, fn)
}

// TestDown_NotFoundIsIdempotentSuccess pins the PR #21 carry-forward:
// a missing instance is NOT a user error — scripts that wrap `down`
// should be able to call it idempotently without checking first.
func TestDown_NotFoundIsIdempotentSuccess(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, DownOptions{Name: "ghost"})
	if code != localnet.ExitSuccess {
		t.Fatalf("code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "Nothing to do") {
		t.Errorf("expected 'Nothing to do' hint, got %q", out.String())
	}
}

// TestDown_AlreadyStoppedIsNoOpSuccess: re-calling on a stopped
// instance must be a no-op success that does NOT call the stopper.
func TestDown_AlreadyStoppedIsNoOpSuccess(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusStopped)

	called := false
	installFakeStopper(t, func(context.Context, *registry.State) error {
		called = true
		return errors.New("stopper should not run on already-stopped instance")
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, DownOptions{Name: "demo"})
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

// TestDown_HappyPath_StatusStoppingBeforeCompose pins the most
// important PR #21 carry-forward (regressed in PR #33, surfaced in
// review): the transitional `stopping` status MUST be persisted
// BEFORE the compose call. Without it, a SIGKILL mid-teardown
// leaves the registry showing "running" while containers are gone.
//
// We assert by having the fake stopper READ the registry mid-call:
// at that point Status must already be StatusStopping. After the
// default down (no --keep-data) the entry is removed so we can
// only observe the transitional state from inside the stopper.
func TestDown_HappyPath_StatusStoppingBeforeCompose(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusRunning)

	var observed registry.Status
	installFakeStopper(t, func(context.Context, *registry.State) error {
		if s, err := registry.Read("demo"); err == nil {
			observed = s.Status
		}
		return nil
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, DownOptions{Name: "demo"})
	if code != localnet.ExitSuccess {
		t.Fatalf("code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if observed != registry.StatusStopping {
		t.Errorf("status during compose call = %q, want %q (PR #21 contract regressed)",
			observed, registry.StatusStopping)
	}
	// Post-success default: instance removed from registry.
	if _, err := registry.Read("demo"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("expected ErrNotFound after default down, got %v", err)
	}
}

// TestDown_KeepData preserves the per-instance registry entry and
// flips status to Stopped. Mirrors PR #21's --keep-data flag.
func TestDown_KeepData(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusRunning)
	installFakeStopper(t, func(context.Context, *registry.State) error { return nil })

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf,
		DownOptions{Name: "demo", KeepData: true})
	if code != localnet.ExitSuccess {
		t.Fatalf("code = %d; stderr=%q", code, errBuf.String())
	}
	got, err := registry.Read("demo")
	if err != nil {
		t.Fatalf("re-read state with --keep-data: %v", err)
	}
	if got.Status != registry.StatusStopped {
		t.Errorf("Status = %q, want %q", got.Status, registry.StatusStopped)
	}
}

// TestDown_ComposeFailureMarksFailedExits4 is the PR #21 cell
// reviewer flagged as collapsed: a genuine compose-down failure
// (NOT interruption) MUST mark StatusFailed and return
// ExitRuntimeFailure (4), preserving the registry entry so the
// user can retry.
func TestDown_ComposeFailureMarksFailedExits4(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusRunning)
	installFakeStopper(t, func(context.Context, *registry.State) error {
		return errors.New("docker daemon vanished")
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, DownOptions{Name: "demo"})
	if code != localnet.ExitRuntimeFailure {
		t.Fatalf("code = %d, want ExitRuntimeFailure (%d); stderr=%q",
			code, localnet.ExitRuntimeFailure, errBuf.String())
	}
	got, err := registry.Read("demo")
	if err != nil {
		t.Fatalf("registry entry should be preserved for retry: %v", err)
	}
	if got.Status != registry.StatusFailed {
		t.Errorf("Status = %q, want %q (PR #21 cell regressed)",
			got.Status, registry.StatusFailed)
	}
	if !strings.Contains(errBuf.String(), "preserved") {
		t.Errorf("stderr should mention state preservation, got %q", errBuf.String())
	}
}

// TestDown_InterruptionMarksPartialExits3 is the OTHER cell PR #33
// collapsed. SIGINT during compose-down must return ExitTimeout (3)
// and mark StatusPartial — distinct from the genuine-failure cell
// because the compose process may still be running on the host.
func TestDown_InterruptionMarksPartialExits3(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusRunning)

	ctx, cancel := context.WithCancel(context.Background())
	installFakeStopper(t, func(c context.Context, _ *registry.State) error {
		cancel() // simulate interruption mid-call
		time.Sleep(5 * time.Millisecond)
		return c.Err() // context.Canceled
	})

	var out, errBuf bytes.Buffer
	code := RunDown(ctx, &out, &errBuf, DownOptions{Name: "demo"})
	if code != localnet.ExitTimeout {
		t.Fatalf("code = %d, want ExitTimeout (%d); stderr=%q",
			code, localnet.ExitTimeout, errBuf.String())
	}
	got, err := registry.Read("demo")
	if err != nil {
		t.Fatalf("registry entry should be preserved for retry: %v", err)
	}
	if got.Status != registry.StatusPartial {
		t.Errorf("Status = %q, want %q (interruption cell regressed)",
			got.Status, registry.StatusPartial)
	}
	if !strings.Contains(errBuf.String(), "Interrupted") {
		t.Errorf("stderr should explain interruption, got %q", errBuf.String())
	}
}

// TestDown_DefaultStopPassesEnvFiles verifies that defaultStop
// provides compose env files to the ComposeRunner (either from the
// persisted overlay.env or adapter fallback), preventing the
// PARTY_HINT interpolation error.
func TestDown_DefaultStopPassesEnvFiles(t *testing.T) {
	dataDir := t.TempDir()
	// Write overlay.env so the preferred path is exercised.
	_, err := localnet.WriteOverlayEnv(dataDir, map[string]string{
		"PARTY_HINT":   "env-test-localparty-1",
		"IMAGE_TAG":    "0.6.4",
		"LOCALNET_DIR": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("WriteOverlayEnv: %v", err)
	}
	st := &registry.State{
		Name:           "env-test",
		SpliceVersion:  "0.6.4",
		ComposeProject: "canton-env-test",
		ProjectDir:     t.TempDir(),
		DataDir:        dataDir,
		ComposeFiles:   []string{"compose.yaml"},
	}
	stopErr := defaultStop(context.Background(), st)
	// Will fail on docker (no real compose project), but must NOT
	// fail with PARTY_HINT interpolation error.
	if stopErr != nil && strings.Contains(stopErr.Error(), "PARTY_HINT") {
		t.Fatalf("defaultStop still fails with PARTY_HINT error: %v", stopErr)
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
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for invalid --name")
	}
}

// TestDown_LockAcquiredBeforeStateBranch is the reviewer pin (PR #33
// #4) for lock-ordering. The original code read state, branched on
// StatusStopped, THEN acquired the lock — racy: a concurrent writer
// flipping status between the Read and the Lock would slip through.
//
// We pin the corrected order with a NEGATIVE assertion: seed an
// already-stopped instance, hold the lock from this goroutine, then
// run `down`. With the bug present, RunDown would observe Stopped
// before the lock and short-circuit ExitSuccess without contending
// the lock. With the fix, RunDown attempts the lock FIRST, gets
// LockHeld immediately (registry.Lock is non-blocking by contract),
// and returns ExitUserError. The exit code is the observable that
// proves the lock came first.
//
// Additionally, the stopper MUST NOT have been called — proving
// neither the stopped-short-circuit nor any later code path was
// reached.
func TestDown_LockAcquiredBeforeStateBranch(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusStopped)

	release, err := registry.Lock("demo")
	if err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	defer release()

	stopperRan := false
	installFakeStopper(t, func(context.Context, *registry.State) error {
		stopperRan = true
		return nil
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf,
		DownOptions{Name: "demo"})

	// Lock-first contract: with the bug, code would be ExitSuccess
	// (short-circuit on the stale Stopped read). With the fix, the
	// lock attempt fails first and surfaces as ExitUserError.
	if code == localnet.ExitSuccess {
		t.Fatalf("RunDown returned ExitSuccess while lock was held — short-circuited on state before Lock (lock-ordering regression). stderr=%q", errBuf.String())
	}
	if code != localnet.ExitUserError {
		t.Fatalf("RunDown returned %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if stopperRan {
		t.Error("stopper ran despite lock contention — lock not held across the call")
	}
}
