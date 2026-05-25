package localnet

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// fakeArchiver is the in-memory volumeArchiver used by every test
// here — no docker required.
type fakeArchiver struct {
	mu      sync.Mutex
	volumes map[string][]byte
	listErr error
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
	// Population matters: ports + credentials need to survive the
	// round-trip via embedded state.json.
	s.Ports = map[string]int{"app_user_ui": 4485}
	s.Credentials = map[string]registry.Credential{
		"sv": {Role: "sv", User: "sv-user", Audience: "sv-aud", JWT: "eyJ.sigsv"},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestSnapshot_RoundTripsRegistryAndVolumes is the full happy path.
// Reviewer flagged that the original cut didn't capture state, so
// this test explicitly asserts the registry IS re-populated after
// restoring into a fresh registry root with no prior instance.
func TestSnapshot_RoundTripsRegistryAndVolumes(t *testing.T) {
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

	// Restore into a FRESH registry root with NO prior `demo`
	// instance. The embedded state.json must re-register it.
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	if _, err := registry.Read("demo"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("test setup: registry should be empty, got %v", err)
	}
	fa2 := &fakeArchiver{volumes: map[string][]byte{}}
	installFakeArchiver(t, fa2)
	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}

	// Volumes restored byte-for-byte.
	for vol, want := range fa.volumes {
		got, ok := fa2.volumes[vol]
		if !ok {
			t.Errorf("restore did not write %q", vol)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("volume %q round-trip mismatch", vol)
		}
	}

	// Registry re-populated from embedded state.json.
	got, err := registry.Read("demo")
	if err != nil {
		t.Fatalf("registry not re-registered: %v", err)
	}
	if got.SpliceVersion != "0.6.4" || got.ComposeProject != "canton-demo" {
		t.Errorf("registry round-trip wrong: %+v", got)
	}
	if got.Status != registry.StatusStopped {
		t.Errorf("restored Status = %q, want stopped", got.Status)
	}
	if got.Ports["app_user_ui"] != 4485 {
		t.Errorf("ports not preserved: %+v", got.Ports)
	}
	if _, hasSv := got.Credentials["sv"]; !hasSv {
		t.Errorf("credentials not preserved: %+v", got.Credentials)
	}
}

// TestRestore_RejectsZipSlip is the security regression for the
// reviewer-flagged Zip Slip vulnerability. A malicious archive
// with `volumes/../../escape.tar` MUST be skipped (not restored
// to a path outside the volumes/ namespace).
func TestRestore_RejectsZipSlip(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}})

	// Hand-craft an archive: valid header + state.json, then a
	// volumes/../../etc/passwd.tar entry trying to escape.
	dest := filepath.Join(t.TempDir(), "evil.tgz")
	writeEvilArchive(t, dest, []evilEntry{
		{name: archiveHeaderName, body: validSnapshotJSON("demo", "0.6.4")},
		{name: archiveStateName, body: validStateJSON("demo")},
		{name: "volumes/../../etc/passwd.tar", body: []byte("ROOTKIT")},
	})

	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false)
	if code != localnet.ExitSuccess {
		t.Fatalf("restore should skip Zip-Slip entry and succeed, got code %d; stderr=%q",
			code, errBuf.String())
	}
	if !strings.Contains(out.String(), "Skipping unknown entry") {
		t.Errorf("expected 'Skipping unknown entry' warning, got %q", out.String())
	}
	// And the rootkit must NOT appear in any fake volume.
	fa := archiverFn.(*fakeArchiver)
	for vol, body := range fa.volumes {
		if bytes.Equal(body, []byte("ROOTKIT")) {
			t.Errorf("Zip Slip body landed in volume %q", vol)
		}
	}
}

// TestRestore_VerifiesContentSHA is the regression for the
// "SHA verification is decorative" finding. A tampered volume body
// — whose recorded SHA in the header is correct but whose bytes on
// the wire are not — MUST fail restore.
func TestRestore_VerifiesContentSHA(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")

	fa := &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": []byte("ORIGINAL")}}
	installFakeArchiver(t, fa)

	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	// Tamper: re-write the volume body inside the archive. We do
	// this by reading the archive, rewriting the volumes/*.tar
	// entry's contents, and writing it back. The header's
	// recorded SHA is unchanged → mismatch on restore.
	tamper(t, dest, "canton-demo_postgres", []byte("TAMPERED"))

	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}})
	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false)
	if code == localnet.ExitSuccess {
		t.Fatal("restore should refuse tampered volume, got ExitSuccess")
	}
	if !strings.Contains(errBuf.String(), "SHA mismatch") {
		t.Errorf("stderr should mention SHA mismatch, got %q", errBuf.String())
	}
}

// TestRestore_RefusesSpliceVersionMismatch covers the
// silent-corruption regression — restoring a 0.6.4 snapshot into
// a 0.7.0 instance now refuses unless --force is set.
func TestRestore_RefusesSpliceVersionMismatch(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo") // 0.6.4
	fa := &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": []byte("x")}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	// Now flip the local instance to 0.7.0 and attempt restore.
	st, _ := registry.Read("demo")
	st.SpliceVersion = "0.7.0"
	if err := registry.Write(st); err != nil {
		t.Fatalf("flip version: %v", err)
	}

	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}})
	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false); code != localnet.ExitUserError {
		t.Fatalf("expected ExitUserError without --force, got %d; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "Splice version mismatch") {
		t.Errorf("stderr should mention version mismatch, got %q", errBuf.String())
	}

	// With --force the same restore should succeed.
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}})
	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, true); code != localnet.ExitSuccess {
		t.Fatalf("--force restore failed: code %d stderr=%q", code, errBuf.String())
	}
}

// TestSnapshot_RejectsOversizedVolume pins the OOM-guard: the
// streaming writer must refuse if a single volume's archive
// exceeds maxArchiveEntry. We can't exercise the real ceiling
// (16 GiB) in tests; we verify the cappedWriter unit instead.
func TestCappedWriter_EnforcesCeiling(t *testing.T) {
	var sink bytes.Buffer
	w := newCappedWriter(&sink, 10)
	if _, err := w.Write([]byte("123456789")); err != nil {
		t.Fatalf("9-byte write should succeed: %v", err)
	}
	if _, err := w.Write([]byte("X")); err != nil {
		t.Fatalf("10th byte exact-fit should succeed: %v", err)
	}
	if _, err := w.Write([]byte("Y")); err == nil || !w.exceeded {
		t.Errorf("11th byte should refuse + flip exceeded")
	}
}

// TestValidateArchivePath covers every cell of the Zip Slip
// safety table. The reviewer flagged this as missing direct
// coverage.
func TestValidateArchivePath(t *testing.T) {
	cases := []struct {
		name    string
		wantVol string
		wantOK  bool
	}{
		{"volumes/canton-demo_postgres.tar", "canton-demo_postgres", true},
		{"volumes/a.tar", "a", true},
		{"volumes/../../etc/passwd.tar", "", false},
		{"volumes/foo/bar.tar", "", false},
		{"volumes/.tar", "", false},
		{"volumes/.hidden.tar", "", false},
		{"snapshot.json", "", false},
		{"state.json", "", false},
		{"./volumes/x.tar", "", false}, // path.Clean would strip the ./
		{"volumes//x.tar", "", false},  // path.Clean would strip the //
		{"", "", false},
		{"volumes/x", "", false},
		// Inner becomes "x.tar" after suffix-strip; "." is a legal
		// non-first char in a docker volume name → accepted.
		{"volumes/x.tar.tar", "x.tar", true},
	}
	for _, c := range cases {
		got, ok := validateArchivePath(c.name)
		if ok != c.wantOK {
			t.Errorf("validateArchivePath(%q) ok = %v, want %v", c.name, ok, c.wantOK)
		}
		if c.wantOK && got != c.wantVol {
			t.Errorf("validateArchivePath(%q) vol = %q, want %q", c.name, got, c.wantVol)
		}
	}
}

func TestValidateVolumeName(t *testing.T) {
	cases := map[string]bool{
		"":                        false,
		"a":                       true,
		"canton-demo_postgres":    true,
		"canton.demo.v1":          true,
		"_starts-with-underscore": false,
		".starts-with-dot":        false,
		"-starts-with-hyphen":     false,
		"has/slash":               false,
		"has space":               false,
		"has$dollar":              false,
		strings.Repeat("a", 64):   true,
		strings.Repeat("a", 65):   false,
	}
	for in, wantOK := range cases {
		err := validateVolumeName(in)
		if (err == nil) != wantOK {
			t.Errorf("validateVolumeName(%q) err=%v, want ok=%v", in, err, wantOK)
		}
	}
}

// TestSnapshot_NotFoundIsUserError is unchanged from the first cut.
func TestSnapshot_NotFoundIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{})
	var out, errBuf bytes.Buffer
	code := RunSnapshot(context.Background(), &out, &errBuf, "ghost",
		filepath.Join(t.TempDir(), "x.tgz"))
	if code != localnet.ExitUserError {
		t.Fatalf("code = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
}

// TestRestore_RefusesRunningInstance still applies.
func TestRestore_RefusesRunningInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": []byte("x")}})
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("setup snapshot failed: %d", code)
	}

	st, _ := registry.Read("demo")
	st.Status = registry.StatusRunning
	_ = registry.Write(st)

	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false)
	if code != localnet.ExitUserError {
		t.Fatalf("code = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
}

// TestRestore_RejectsMissingFile — typo path.
func TestRestore_RejectsMissingFile(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{})
	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", "/nonexistent/path.tgz", false)
	if code != localnet.ExitUserError {
		t.Fatalf("code = %d, want ExitUserError", code)
	}
}

// ─── archive-writing test helpers ──────────────────────────────

type evilEntry struct {
	name string
	body []byte
}

func writeEvilArchive(t *testing.T, dest string, entries []evilEntry) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatalf("create %s: %v", dest, err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name, Size: int64(len(e.body)), Mode: 0o600, ModTime: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatalf("write body %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gz: %v", err)
	}
}

func validSnapshotJSON(name, spliceVersion string) []byte {
	return []byte(`{"schema_version":1,"instance":"` + name +
		`","splice_version":"` + spliceVersion +
		`","created_at":"2026-05-25T00:00:00Z","devkit_version":"test","volumes":[]}`)
}

func validStateJSON(name string) []byte {
	return []byte(`{"schema_version":1,"name":"` + name +
		`","splice_version":"0.6.4","created_at":"2026-05-25T00:00:00Z",` +
		`"compose_project":"canton-` + name + `","compose_files":[],` +
		`"docker_network":"` + name + `","container_prefix":"` + name + `-",` +
		`"project_dir":"/tmp/p","data_dir":"/tmp/d","ports":{},"status":"stopped"}`)
}

// tamper opens an existing snapshot.tgz, replaces the body of the
// volumes/<vol>.tar entry with `body` (re-using the original
// size), and writes back. We bypass writeEvilArchive because we
// need to copy through the unrelated entries (header / state).
func tamper(t *testing.T, archivePath, vol string, body []byte) {
	t.Helper()
	in, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open %s: %v", archivePath, err)
	}
	defer func() { _ = in.Close() }()
	gzr, err := gzip.NewReader(in)
	if err != nil {
		t.Fatalf("gzip read %s: %v", archivePath, err)
	}
	tr := tar.NewReader(gzr)

	tmp := archivePath + ".tamper"
	out, err := os.Create(tmp)
	if err != nil {
		t.Fatalf("create %s: %v", tmp, err)
	}
	gzw := gzip.NewWriter(out)
	tw := tar.NewWriter(gzw)
	target := "volumes/" + vol + ".tar"
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read entry: %v", err)
		}
		newBody, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if hdr.Name == target {
			// Pad / truncate to the original size to preserve the
			// header.Size field. cap len(body) at original.
			if len(body) > len(newBody) {
				body = body[:len(newBody)]
			} else if len(body) < len(newBody) {
				body = append(body, bytes.Repeat([]byte{0}, len(newBody)-len(body))...)
			}
			newBody = body
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write(newBody); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	_ = tw.Close()
	_ = gzw.Close()
	_ = out.Close()
	_ = gzr.Close()
	_ = in.Close()
	if err := os.Rename(tmp, archivePath); err != nil {
		t.Fatalf("rename: %v", err)
	}
}
