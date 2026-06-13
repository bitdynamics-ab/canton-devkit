package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestRunDown_PersistsStoppingBeforeCompose locks in the contract:
// RunDown must persist Status=stopping BEFORE calling `docker compose
// down`, otherwise a crash mid-teardown leaves the registry reporting
// `running` while the host is in an indeterminate state.
//
// The test injects a downer stub that, when invoked, reads the
// on-disk state.json and records what Status it observes. If RunDown
// fails to persist the transition before invoking compose, the stub
// would see Status=running and the assertion would fire.
func TestRunDown_PersistsStoppingBeforeCompose(t *testing.T) {
	name := "stopping-state"
	seedRunningInstance(t, name)

	var observed registry.Status
	opts := &DownOptions{
		Name: name,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(context.Context) error {
				// Peek at state at the exact moment compose is invoked.
				s, err := registry.Read(name)
				if err != nil {
					t.Fatalf("registry.Read during compose: %v", err)
				}
				observed = s.Status
				return nil
			})
		},
	}

	var out, errBuf bytes.Buffer
	if code := RunDown(context.Background(), &out, &errBuf, opts); code != ExitSuccess {
		t.Fatalf("RunDown = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if observed != registry.StatusStopping {
		t.Errorf("observed Status at compose-down time = %q, want %q (transitional state not persisted before compose)",
			observed, registry.StatusStopping)
	}
}

// TestRunDown_PreservesStateOnComposeFailure: a `docker compose down`
// failure must NOT silently fall through to registry.Delete. The state
// must stay on disk with Status=failed and the command must exit
// non-zero so the user can retry cleanup instead of losing the metadata
// while resources are potentially still running.
func TestRunDown_PreservesStateOnComposeFailure(t *testing.T) {
	name := "compose-fails"
	seedRunningInstance(t, name)

	opts := &DownOptions{
		Name: name,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(context.Context) error {
				return errors.New("docker daemon unreachable")
			})
		},
	}

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, opts)
	if code != ExitRuntimeFailure {
		t.Fatalf("RunDown = %d, want ExitRuntimeFailure; stderr=%q", code, errBuf.String())
	}

	// Registry entry MUST still exist with Status=failed; a prior
	// implementation deleted it after the warning and returned 0,
	// which is the bug this guards against.
	s, err := registry.Read(name)
	if err != nil {
		t.Fatalf("registry.Read after compose failure: %v (state was deleted — regression)", err)
	}
	if s.Status != registry.StatusFailed {
		t.Errorf("Status after compose failure = %q, want %q", s.Status, registry.StatusFailed)
	}
	// The data dir must still exist too — losing it would cost
	// container logs the user might want for debugging.
	if _, err := os.Stat(s.DataDir); err != nil {
		t.Errorf("DataDir missing after compose failure: %v (data must be preserved for retry)", err)
	}
	// Error message must mention the retry path so the user knows what to do.
	if !strings.Contains(errBuf.String(), "Re-run") {
		t.Errorf("stderr should hint at retry, got %q", errBuf.String())
	}
}

// TestRunDown_InterruptionExitsTimeout pins the second cell of the
// compose-failure decision table: if ctx is canceled mid-`compose
// down`, we return ExitTimeout (not ExitRuntimeFailure) and persist
// the transitional state as `partial` since the host may still have
// work in flight.
func TestRunDown_InterruptionExitsTimeout(t *testing.T) {
	name := "down-interrupted"
	seedRunningInstance(t, name)

	ctx, cancel := context.WithCancel(context.Background())
	opts := &DownOptions{
		Name: name,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(c context.Context) error {
				cancel() // simulate the user pressing Ctrl-C mid-teardown
				return c.Err()
			})
		},
	}

	var out, errBuf bytes.Buffer
	code := RunDown(ctx, &out, &errBuf, opts)
	if code != ExitTimeout {
		t.Fatalf("RunDown = %d, want ExitTimeout; stderr=%q", code, errBuf.String())
	}
	s, err := registry.Read(name)
	if err != nil {
		t.Fatalf("registry.Read after interruption: %v", err)
	}
	if s.Status != registry.StatusPartial {
		t.Errorf("Status after interruption = %q, want %q", s.Status, registry.StatusPartial)
	}
}

// TestRunDown_HappyPathPreservesRegistry is the success counterpart —
// when compose down returns nil, the registry entry is preserved with
// status=stopped so that a subsequent `localnet clean` can discover
// the compose project and remove Docker volumes.
func TestRunDown_HappyPathPreservesRegistry(t *testing.T) {
	name := "down-happy"
	seedRunningInstance(t, name)

	opts := &DownOptions{
		Name: name,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
			return downerFn(func(context.Context) error { return nil })
		},
	}

	var out, errBuf bytes.Buffer
	if code := RunDown(context.Background(), &out, &errBuf, opts); code != ExitSuccess {
		t.Fatalf("RunDown = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	s, err := registry.Read(name)
	if err != nil {
		t.Fatalf("registry entry should be preserved after down, got err=%v", err)
	}
	if s.Status != registry.StatusStopped {
		t.Errorf("Status = %q, want %q", s.Status, registry.StatusStopped)
	}
}

// seedRunningInstance writes a minimal `running` registry entry +
// data dir for a synthetic instance so RunDown has something to tear
// down. Uses t.TempDir for both the registry root and the data dir.
func seedRunningInstance(t *testing.T, name string) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	dataDir := registry.DataDirFor(name)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	// Touch a sentinel file so we can prove DataDir survives compose failure.
	if err := os.WriteFile(filepath.Join(dataDir, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	state := registry.NewState(name, "0.6.4")
	state.ComposeProject = "canton-" + name
	state.ComposeFiles = []string{"/nonexistent/compose.yaml"} // never read in tests
	state.DataDir = dataDir
	state.ProjectDir = t.TempDir()
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		t.Fatalf("seed registry.Write: %v", err)
	}
}

// downerFn adapts a function to composeDowner so tests can inline the
// stub body without a struct.
type downerFn func(context.Context) error

func (f downerFn) Down(ctx context.Context) error { return f(ctx) }
