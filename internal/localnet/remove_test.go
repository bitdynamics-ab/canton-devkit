package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedInstanceStatus writes a minimal registry entry with a chosen
// status into the CURRENT registry root (caller sets the root once
// via t.Setenv so multiple instances share it — needed for --all).
func seedInstanceStatus(t *testing.T, name string, status registry.Status) {
	t.Helper()
	dataDir := registry.DataDirFor(name)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.ComposeFiles = []string{"/nonexistent/compose.yaml"}
	s.DataDir = dataDir
	s.ProjectDir = t.TempDir()
	s.Status = status
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// nilDowner returns a RemoveOptions runner factory whose Down() always
// succeeds — the common case (containers already gone / teardown OK).
func nilDowner() func(string, []string, []string, []string, string, io.Writer) composeDowner {
	return func(string, []string, []string, []string, string, io.Writer) composeDowner {
		return downerFn(func(context.Context) error { return nil })
	}
}

func TestRunRemove_StoppedInstanceDeletesRegistry(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "stopped-one", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:      "stopped-one",
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("stopped-one"); err != registry.ErrNotFound {
		t.Errorf("registry entry should be gone, got err=%v", err)
	}
}

func TestRunRemove_RunningRefusedWithoutForce(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-one", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:      "live-one",
		NewRunner: nilDowner(),
	})
	if code != ExitUserError {
		t.Fatalf("RunRemove = %d, want ExitUserError", code)
	}
	// Registry must be PRESERVED — we refused.
	if _, err := registry.Read("live-one"); err != nil {
		t.Errorf("running instance must survive a refused remove, got err=%v", err)
	}
}

func TestRunRemove_RunningConfirmedTearsDownAndDeletes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-yes", registry.StatusRunning)

	asked := ""
	downCalled := false
	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name: "live-yes",
		ConfirmStop: func(name string) (bool, error) {
			asked = name
			return true, nil
		},
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return removeVolumesCapture(func(_ context.Context, removeVolumes bool) error {
				downCalled = true
				if !removeVolumes {
					t.Error("confirmed remove must pass removeVolumes=true")
				}
				return nil
			})
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if asked != "live-yes" {
		t.Errorf("ConfirmStop asked about %q, want %q", asked, "live-yes")
	}
	if !downCalled {
		t.Error("a confirmed remove must tear the running instance down")
	}
	if _, err := registry.Read("live-yes"); err != registry.ErrNotFound {
		t.Errorf("registry entry should be gone after a confirmed remove, got err=%v", err)
	}
}

func TestRunRemove_RunningDeclinedKeepsInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-no", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:        "live-no",
		ConfirmStop: func(string) (bool, error) { return false, nil },
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(context.Context) error {
				t.Error("declined remove must not tear anything down")
				return nil
			})
		},
	})
	if code != ExitUserError {
		t.Fatalf("RunRemove = %d, want ExitUserError", code)
	}
	if _, err := registry.Read("live-no"); err != nil {
		t.Errorf("declined remove must preserve the instance, got err=%v", err)
	}
}

// A ConfirmStop that cannot ask (non-interactive stdin) surfaces its
// error and leaves the instance alone.
func TestRunRemove_ConfirmErrorKeepsInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-err", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:        "live-err",
		ConfirmStop: func(string) (bool, error) { return false, errors.New("stdin is not a terminal") },
		NewRunner:   nilDowner(),
	})
	if code != ExitUserError {
		t.Fatalf("RunRemove = %d, want ExitUserError", code)
	}
	if !bytes.Contains(errBuf.Bytes(), []byte("stdin is not a terminal")) {
		t.Errorf("confirmation error should be surfaced; got %q", errBuf.String())
	}
	if _, err := registry.Read("live-err"); err != nil {
		t.Errorf("unconfirmed remove must preserve the instance, got err=%v", err)
	}
}

func TestRunRemove_RunningForcedTearsDownAndDeletes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-force", registry.StatusRunning)

	downCalled := false
	removedVolumes := false
	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:  "live-force",
		Force: true,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return removeVolumesCapture(func(_ context.Context, removeVolumes bool) error {
				downCalled = true
				removedVolumes = removeVolumes
				return nil
			})
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if !downCalled {
		t.Error("--force remove must invoke compose down on a running instance")
	}
	// `remove` is the destructive verb: unlike `down`, it MUST remove the
	// ledger volumes (the symmetric half of the data-loss fix).
	if !removedVolumes {
		t.Error("--force remove must pass removeVolumes=true to reclaim ledger volumes")
	}
	if _, err := registry.Read("live-force"); err != registry.ErrNotFound {
		t.Errorf("registry entry should be gone after forced remove, got err=%v", err)
	}
}

func TestRunRemove_ForceTeardownFailurePreservesState(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-fail", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:  "live-fail",
		Force: true,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(context.Context) error { return context.DeadlineExceeded })
		},
	})
	if code != ExitRuntimeFailure {
		t.Fatalf("RunRemove = %d, want ExitRuntimeFailure", code)
	}
	// A failed forced teardown must NOT scrub state — that would
	// orphan live containers.
	if _, err := registry.Read("live-fail"); err != nil {
		t.Errorf("state must survive a failed --force teardown, got err=%v", err)
	}
}

// downerWithContainers is a composeDowner whose Stop fails and that
// reports a fixed set of still-present containers — exercising remove's
// "don't scrub the registry while containers linger" guard.
type downerWithContainers struct{ remaining []string }

func (d downerWithContainers) Stop(context.Context, bool) error { return context.DeadlineExceeded }

func (d downerWithContainers) RemainingContainers(context.Context) ([]string, error) {
	return d.remaining, nil
}

// A STOPPED instance whose teardown errors AND still has containers must
// keep its registry entry — deleting it would orphan those containers
// (the exact bug behind the e2e-metrics-demo-style orphans).
func TestRunRemove_StoppedTeardownFailureWithLingeringContainersPreservesRegistry(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "stuck", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name: "stuck",
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerWithContainers{remaining: []string{"c1", "c2"}}
		},
	})
	if code != ExitRuntimeFailure {
		t.Fatalf("RunRemove = %d, want ExitRuntimeFailure (containers linger); stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("stuck"); err != nil {
		t.Errorf("registry must be preserved when containers remain, got err=%v", err)
	}
}

// The lenient case stays lenient: a teardown error with NO remaining
// containers (the usual "no such project" for an already-gone instance)
// still scrubs the registry.
func TestRunRemove_StoppedTeardownErrorButContainersGoneStillDeletes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "gone", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name: "gone",
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerWithContainers{remaining: nil}
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess (containers already gone); stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("gone"); err != registry.ErrNotFound {
		t.Errorf("registry should be scrubbed when no containers remain, got err=%v", err)
	}
}

func TestRunRemove_DryRunChangesNothing(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "dry-one", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:      "dry-one",
		DryRun:    true,
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("dry-one"); err != nil {
		t.Errorf("dry-run must not delete the instance, got err=%v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("would remove")) {
		t.Errorf("dry-run output should describe the plan; got %q", out.String())
	}
}

func TestRunRemove_DryRunRunningReportsPromptWithoutAsking(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "dry-live", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:   "dry-live",
		DryRun: true,
		ConfirmStop: func(string) (bool, error) {
			t.Error("dry-run must not ask for confirmation")
			return false, nil
		},
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("would ask to tear it down first")) {
		t.Errorf("dry-run should say the running instance would be confirmed; got %q", out.String())
	}
	if _, err := registry.Read("dry-live"); err != nil {
		t.Errorf("dry-run must not remove the instance, got err=%v", err)
	}
}

func TestRunRemove_DryRunOrphanDoesNotCreateLockArtifacts(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "dry-ghost", registry.StatusStopped)
	dataDir := registry.DataDirFor("dry-ghost")
	// Leave the index entry behind but remove the state directory. A
	// dry-run must report the planned scrub without recreating the dir.
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatalf("wipe dataDir: %v", err)
	}

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:      "dry-ghost",
		DryRun:    true,
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not recreate orphan data dir, stat err=%v", err)
	}
	idx, _ := registry.ReadIndex()
	found := false
	for _, e := range idx.Entries {
		if e.Name == "dry-ghost" {
			found = true
		}
	}
	if !found {
		t.Error("dry-run must not scrub the orphan index entry")
	}
	if !bytes.Contains(out.Bytes(), []byte("would scrub orphan registry entry")) {
		t.Errorf("dry-run output should describe orphan scrub; got %q", out.String())
	}
}

func TestRunRemove_MissingNameDoesNotCreateLockArtifacts(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	dataDir := registry.DataDirFor("never-registered")

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:      "never-registered",
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("missing-name remove must not create data dir, stat err=%v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("No instance named \"never-registered\" is registered")) {
		t.Errorf("missing-name output should say no instance is registered; got %q", out.String())
	}
}

func TestRunRemove_OrphanIndexEntryScrubbed(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "ghost", registry.StatusStopped)
	// Wipe the data dir (incl. state.json) but leave the index entry —
	// the orphan condition remove must repair.
	if err := os.RemoveAll(registry.DataDirFor("ghost")); err != nil {
		t.Fatalf("wipe dataDir: %v", err)
	}
	if _, err := registry.Read("ghost"); err != registry.ErrNotFound {
		t.Fatalf("setup: ghost should be unreadable, got %v", err)
	}

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		Name:      "ghost",
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRemove = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	idx, _ := registry.ReadIndex()
	for _, e := range idx.Entries {
		if e.Name == "ghost" {
			t.Error("orphan index entry should be scrubbed")
		}
	}
}

func TestRunRemove_AllSweep(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "a-stopped", registry.StatusStopped)
	seedInstanceStatus(t, "b-failed", registry.StatusFailed)
	seedInstanceStatus(t, "c-running", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunRemove(context.Background(), &out, &errBuf, &RemoveOptions{
		All:       true,
		NewRunner: nilDowner(),
	})
	// One running instance refused → soft ExitUserError overall.
	if code != ExitUserError {
		t.Fatalf("RunRemove --all = %d, want ExitUserError (running refused)", code)
	}
	// Stopped + failed removed; running preserved.
	if _, err := registry.Read("a-stopped"); err != registry.ErrNotFound {
		t.Errorf("a-stopped should be removed, got %v", err)
	}
	if _, err := registry.Read("b-failed"); err != registry.ErrNotFound {
		t.Errorf("b-failed should be removed, got %v", err)
	}
	if _, err := registry.Read("c-running"); err != nil {
		t.Errorf("c-running should be preserved (refused), got %v", err)
	}
}
