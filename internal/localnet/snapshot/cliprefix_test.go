package snapshot

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func snapshotAndRestoreOutput(t *testing.T) (snap, restore string) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusRunning)
	installFake(t, &FakeArchiver{Dump: []byte("-- dump\n"), DBCount: 1})
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot code=%d stderr=%q", code, errBuf.String())
	}
	snap = out.String()

	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusStopped)
	installFake(t, &FakeArchiver{})
	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code=%d stderr=%q", code, errBuf.String())
	}
	return snap, out.String()
}

func TestSnapshotRestore_PromptDefaultsToDPM(t *testing.T) {
	snap, restore := snapshotAndRestoreOutput(t)
	if !strings.Contains(snap, "dpm localnet snapshot demo") {
		t.Errorf("snapshot prompt should echo the dpm command, got %q", snap)
	}
	if !strings.Contains(restore, "dpm localnet restore demo") {
		t.Errorf("restore prompt should echo the dpm command, got %q", restore)
	}
}

func TestSnapshotRestore_PromptFollowsDirectPrefix(t *testing.T) {
	t.Cleanup(cliprefix.Set("canton-devkit localnet"))
	snap, restore := snapshotAndRestoreOutput(t)
	if !strings.Contains(snap, "canton-devkit localnet snapshot demo") || strings.Contains(snap, "dpm localnet") {
		t.Errorf("snapshot prompt should echo the canton-devkit command, got %q", snap)
	}
	if !strings.Contains(restore, "canton-devkit localnet restore demo") || strings.Contains(restore, "dpm localnet") {
		t.Errorf("restore prompt should echo the canton-devkit command, got %q", restore)
	}
}
