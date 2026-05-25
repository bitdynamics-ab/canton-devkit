package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// fakeArchiver is the in-memory volumeArchiver used by every test
// here — no docker required. Stores each "volume" as a byte slice
// keyed by name; ListVolumes returns the keys in sorted order to
// match what production dockerVolumeArchiver does once we add
// sort.Strings on the docker output (snapshot.go also sorts).
type fakeArchiver struct {
	mu      sync.Mutex
	volumes map[string][]byte // contents keyed by volume name
	listErr error             // optional injectable error for ListVolumes
}

func (f *fakeArchiver) ListVolumes(_ context.Context, _ string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.volumes))
	for k := range f.volumes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeArchiver) ArchiveVolume(_ context.Context, volume string, w io.Writer) error {
	f.mu.Lock()
	body, ok := f.volumes[volume]
	f.mu.Unlock()
	if !ok {
		return errors.New("unknown volume")
	}
	_, err := w.Write(body)
	return err
}

func (f *fakeArchiver) RestoreVolume(_ context.Context, volume string, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.volumes == nil {
		f.volumes = map[string][]byte{}
	}
	f.volumes[volume] = body
	return nil
}

func installFakeArchiver(t *testing.T, fa *fakeArchiver) {
	t.Helper()
	prev := archiverFn
	archiverFn = fa
	t.Cleanup(func() { archiverFn = prev })
}

func seedSnapshotInstance(t *testing.T, name string) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = registry.StatusStopped
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestSnapshot_RoundTripsViaFakeArchiver is the full happy path:
// snapshot writes an archive, restore reads it back, and the bytes
// each volume saw are identical. Proves the on-disk format is
// self-describing (no out-of-band state needed to restore).
func TestSnapshot_RoundTripsViaFakeArchiver(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")

	fa := &fakeArchiver{volumes: map[string][]byte{
		"canton-demo_postgres":   []byte("PG TARBALL CONTENT"),
		"canton-demo_canton-vol": []byte("CANTON TARBALL CONTENT"),
	}}
	installFakeArchiver(t, fa)

	dest := filepath.Join(t.TempDir(), "snap.tgz")

	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}

	// Restore into a fresh fakeArchiver so we know the bytes come
	// from the archive, not from leftover state.
	fa2 := &fakeArchiver{volumes: map[string][]byte{}}
	installFakeArchiver(t, fa2)

	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("restore code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}

	for vol, want := range fa.volumes {
		got, ok := fa2.volumes[vol]
		if !ok {
			t.Errorf("restore did not write %q", vol)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("volume %q round-trip mismatch:\n  want %q\n  got  %q", vol, want, got)
		}
	}
}

// TestSnapshot_NotFoundIsUserError covers the unknown-instance path.
func TestSnapshot_NotFoundIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{})

	var out, errBuf bytes.Buffer
	code := RunSnapshot(context.Background(), &out, &errBuf, "ghost", filepath.Join(t.TempDir(), "x.tgz"))
	if code != localnet.ExitUserError {
		t.Fatalf("code = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), `"ghost"`) {
		t.Errorf("stderr should name the missing instance, got %q", errBuf.String())
	}
}

// TestSnapshot_ListFailureBubblesUp verifies that ListVolumes
// returning an error stops the snapshot cleanly with no partial
// file. Otherwise users would be stuck wondering whether half a
// snapshot is on disk.
func TestSnapshot_ListFailureBubblesUp(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	installFakeArchiver(t, &fakeArchiver{listErr: errors.New("docker unreachable")})

	dest := filepath.Join(t.TempDir(), "snap.tgz")

	var out, errBuf bytes.Buffer
	code := RunSnapshot(context.Background(), &out, &errBuf, "demo", dest)
	if code != localnet.ExitRuntimeFailure {
		t.Fatalf("code = %d, want ExitRuntimeFailure; stderr=%q", code, errBuf.String())
	}
}

// TestRestore_RefusesRunningInstance pins the safety contract: we
// don't restore into a live instance because the half-overlap
// between the running service and our `docker run -v` writer is
// undefined behaviour. User must `down` first.
func TestRestore_RefusesRunningInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	// First write a snapshot file we can attempt to restore.
	seedSnapshotInstance(t, "demo")
	fa := &fakeArchiver{volumes: map[string][]byte{"v1": []byte("data")}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("setup snapshot failed with code %d", code)
	}

	// Flip the instance to running.
	st, _ := registry.Read("demo")
	st.Status = registry.StatusRunning
	if err := registry.Write(st); err != nil {
		t.Fatalf("flip status: %v", err)
	}

	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", dest)
	if code != localnet.ExitUserError {
		t.Fatalf("code = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "running") {
		t.Errorf("stderr should explain why, got %q", errBuf.String())
	}
}

// TestRestore_RejectsMissingFile covers the "user typo" path.
func TestRestore_RejectsMissingFile(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{})

	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", "/nonexistent/path.tgz")
	if code != localnet.ExitUserError {
		t.Fatalf("code = %d, want ExitUserError", code)
	}
}
