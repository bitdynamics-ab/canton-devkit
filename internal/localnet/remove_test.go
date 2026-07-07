package localnet

import (
	"bytes"
	"context"
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

// nilDowner returns a CleanOptions runner factory whose Down() always
// succeeds — the common case (containers already gone / teardown OK).
func nilDowner() func(string, []string, []string, []string, string, io.Writer) composeDowner {
	return func(string, []string, []string, []string, string, io.Writer) composeDowner {
		return downerFn(func(context.Context) error { return nil })
	}
}

func TestRunClean_StoppedInstanceDeletesRegistry(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "stopped-one", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:      "stopped-one",
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunClean = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("stopped-one"); err != registry.ErrNotFound {
		t.Errorf("registry entry should be gone, got err=%v", err)
	}
}

func TestRunClean_RunningRefusedWithoutForce(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-one", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:      "live-one",
		NewRunner: nilDowner(),
	})
	if code != ExitUserError {
		t.Fatalf("RunClean = %d, want ExitUserError", code)
	}
	// Registry must be PRESERVED — we refused.
	if _, err := registry.Read("live-one"); err != nil {
		t.Errorf("running instance must survive a refused clean, got err=%v", err)
	}
}

func TestRunClean_RunningForcedTearsDownAndDeletes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-force", registry.StatusRunning)

	downCalled := false
	removedVolumes := false
	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
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
		t.Fatalf("RunClean = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if !downCalled {
		t.Error("--force clean must invoke compose down on a running instance")
	}
	// `clean` is the destructive verb: unlike `down`, it MUST remove the
	// ledger volumes (the symmetric half of the data-loss fix).
	if !removedVolumes {
		t.Error("--force clean must pass removeVolumes=true to reclaim ledger volumes")
	}
	if _, err := registry.Read("live-force"); err != registry.ErrNotFound {
		t.Errorf("registry entry should be gone after forced clean, got err=%v", err)
	}
}

func TestRunClean_ForceTeardownFailurePreservesState(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "live-fail", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:  "live-fail",
		Force: true,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(context.Context) error { return context.DeadlineExceeded })
		},
	})
	if code != ExitRuntimeFailure {
		t.Fatalf("RunClean = %d, want ExitRuntimeFailure", code)
	}
	// A failed forced teardown must NOT scrub state — that would
	// orphan live containers.
	if _, err := registry.Read("live-fail"); err != nil {
		t.Errorf("state must survive a failed --force teardown, got err=%v", err)
	}
}

// downerWithContainers is a composeDowner whose Stop fails and that
// reports a fixed set of still-present containers — exercising clean's
// "don't scrub the registry while containers linger" guard.
type downerWithContainers struct{ remaining []string }

func (d downerWithContainers) Stop(context.Context, bool) error { return context.DeadlineExceeded }

func (d downerWithContainers) RemainingContainers(context.Context) ([]string, error) {
	return d.remaining, nil
}

// A STOPPED instance whose teardown errors AND still has containers must
// keep its registry entry — deleting it would orphan those containers
// (the exact bug behind the e2e-metrics-demo-style orphans).
func TestRunClean_StoppedTeardownFailureWithLingeringContainersPreservesRegistry(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "stuck", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name: "stuck",
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerWithContainers{remaining: []string{"c1", "c2"}}
		},
	})
	if code != ExitRuntimeFailure {
		t.Fatalf("RunClean = %d, want ExitRuntimeFailure (containers linger); stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("stuck"); err != nil {
		t.Errorf("registry must be preserved when containers remain, got err=%v", err)
	}
}

// The lenient case stays lenient: a teardown error with NO remaining
// containers (the usual "no such project" for an already-gone instance)
// still scrubs the registry.
func TestRunClean_StoppedTeardownErrorButContainersGoneStillDeletes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "gone", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name: "gone",
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerWithContainers{remaining: nil}
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunClean = %d, want ExitSuccess (containers already gone); stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("gone"); err != registry.ErrNotFound {
		t.Errorf("registry should be scrubbed when no containers remain, got err=%v", err)
	}
}

func TestRunClean_DryRunChangesNothing(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "dry-one", registry.StatusStopped)

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:      "dry-one",
		DryRun:    true,
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunClean = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if _, err := registry.Read("dry-one"); err != nil {
		t.Errorf("dry-run must not delete the instance, got err=%v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("would remove")) {
		t.Errorf("dry-run output should describe the plan; got %q", out.String())
	}
}

func TestRunClean_DryRunOrphanDoesNotCreateLockArtifacts(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "dry-ghost", registry.StatusStopped)
	dataDir := registry.DataDirFor("dry-ghost")
	// Leave the index entry behind but remove the state directory. A
	// dry-run must report the planned scrub without recreating the dir.
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatalf("wipe dataDir: %v", err)
	}

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:      "dry-ghost",
		DryRun:    true,
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunClean = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
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

func TestRunClean_MissingNameDoesNotCreateLockArtifacts(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	dataDir := registry.DataDirFor("never-registered")

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:      "never-registered",
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunClean = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("missing-name clean must not create data dir, stat err=%v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("No instance named \"never-registered\" is registered")) {
		t.Errorf("missing-name output should say no instance is registered; got %q", out.String())
	}
}

func TestRunClean_OrphanIndexEntryScrubbed(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "ghost", registry.StatusStopped)
	// Wipe the data dir (incl. state.json) but leave the index entry —
	// the orphan condition clean must repair.
	if err := os.RemoveAll(registry.DataDirFor("ghost")); err != nil {
		t.Fatalf("wipe dataDir: %v", err)
	}
	if _, err := registry.Read("ghost"); err != registry.ErrNotFound {
		t.Fatalf("setup: ghost should be unreadable, got %v", err)
	}

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:      "ghost",
		NewRunner: nilDowner(),
	})
	if code != ExitSuccess {
		t.Fatalf("RunClean = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	idx, _ := registry.ReadIndex()
	for _, e := range idx.Entries {
		if e.Name == "ghost" {
			t.Error("orphan index entry should be scrubbed")
		}
	}
}

func TestRunClean_AllSweep(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceStatus(t, "a-stopped", registry.StatusStopped)
	seedInstanceStatus(t, "b-failed", registry.StatusFailed)
	seedInstanceStatus(t, "c-running", registry.StatusRunning)

	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		All:       true,
		NewRunner: nilDowner(),
	})
	// One running instance refused → soft ExitUserError overall.
	if code != ExitUserError {
		t.Fatalf("RunClean --all = %d, want ExitUserError (running refused)", code)
	}
	// Stopped + failed cleaned; running preserved.
	if _, err := registry.Read("a-stopped"); err != registry.ErrNotFound {
		t.Errorf("a-stopped should be cleaned, got %v", err)
	}
	if _, err := registry.Read("b-failed"); err != registry.ErrNotFound {
		t.Errorf("b-failed should be cleaned, got %v", err)
	}
	if _, err := registry.Read("c-running"); err != nil {
		t.Errorf("c-running should be preserved (refused), got %v", err)
	}
}
