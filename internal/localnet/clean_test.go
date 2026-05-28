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
	var out, errBuf bytes.Buffer
	code := RunClean(context.Background(), &out, &errBuf, &CleanOptions{
		Name:  "live-force",
		Force: true,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(context.Context) error { downCalled = true; return nil })
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunClean = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if !downCalled {
		t.Error("--force clean must invoke compose down on a running instance")
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
