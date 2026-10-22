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

func TestSnapshotRestore_HintsFollowCLIPrefix(t *testing.T) {
	for _, tc := range []struct{ set, prefix, other string }{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			if tc.set != "" {
				t.Cleanup(cliprefix.Set(tc.set))
			}
			check := func(label, got string, want ...string) {
				t.Helper()
				for _, w := range want {
					if !strings.Contains(got, w) {
						t.Errorf("%s: missing %q in %q", label, w, got)
					}
				}
				if strings.Contains(got, tc.other) {
					t.Errorf("%s: must not mention %q: %q", label, tc.other, got)
				}
				if strings.Contains(got, "`localnet ") {
					t.Errorf("%s: prefix-less hint in %q", label, got)
				}
			}

			snap, restore := snapshotAndRestoreOutput(t)
			check("snapshot box", snap, tc.prefix+" restore demo --from ")
			check("restore box", restore, "Run "+tc.prefix+" up demo to bring it up.")

			t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
			seedInstance(t, "idle", "0.6.4", registry.StatusStopped)
			installFake(t, &FakeArchiver{})
			var out, errBuf bytes.Buffer
			if code := RunSnapshot(context.Background(), &out, &errBuf, "idle", filepath.Join(t.TempDir(), "s.tgz")); code != localnet.ExitUserError {
				t.Fatalf("snapshot of a stopped instance code=%d", code)
			}
			check("snapshot stopped", errBuf.String(), "Run `"+tc.prefix+" up idle` first.")

			dump := []byte("dump")
			archive := func(name string) string {
				dst := filepath.Join(t.TempDir(), name+".tgz")
				writeArchive(t, dst, validHeader(name, "0.6.4", dump), stateFor(t, name, "0.6.4"), dump)
				return dst
			}

			errBuf.Reset()
			if code := RunRestore(context.Background(), &out, &errBuf, "ghost", archive("ghost"), false); code != localnet.ExitUserError {
				t.Fatalf("restore into unknown instance code=%d", code)
			}
			check("restore unknown", errBuf.String(), "Run `"+tc.prefix+" up ghost` first, then restore.")

			seedInstance(t, "live", "0.6.4", registry.StatusRunning)
			errBuf.Reset()
			if code := RunRestore(context.Background(), &out, &errBuf, "live", archive("live"), false); code != localnet.ExitUserError {
				t.Fatalf("restore into running instance code=%d", code)
			}
			check("restore running", errBuf.String(), "run `"+tc.prefix+" down live` first")

			installFake(t, &FakeArchiver{Running: true})
			errBuf.Reset()
			if code := RunRestore(context.Background(), &out, &errBuf, "idle", archive("idle"), false); code != localnet.ExitUserError {
				t.Fatalf("restore with live containers code=%d", code)
			}
			check("restore live containers", errBuf.String(), "still has running containers — run `"+tc.prefix+" down idle` first")
		})
	}
}
