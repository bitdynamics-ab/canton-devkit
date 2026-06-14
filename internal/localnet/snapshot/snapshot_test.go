package snapshot

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

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// fakeArchiver is the in-memory volumeArchiver used by every test
// here — no docker required.
type fakeArchiver struct {
	mu      sync.Mutex
	volumes map[string][]byte
	listErr error
	// labels records the (project, suffix) RestoreVolume was asked to
	// stamp, keyed by volume name — so tests can assert the Compose
	// labels are re-applied on restore.
	labels map[string]restoreLabels
	// volStorageBytes / volStorageErr drive
	// VolumeStorageAvailableBytes; zero/nil means "report a large,
	// always-passing value" so disk-preflight tests opt in explicitly.
	volStorageBytes uint64
	volStorageErr   error
}

type restoreLabels struct {
	project string
	suffix  string
}

func (f *fakeArchiver) ListVolumes(_ context.Context, composeProject string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.volumes))
	for k := range f.volumes {
		// Mirror production's label filter: ListVolumes selects on
		// com.docker.compose.project. If a volume has recorded labels
		// (i.e. it was created via RestoreVolume), honor them so the
		// round-trip (snapshot→restore→snapshot) is
		// faithful — a restored volume missing the project label must
		// NOT appear here. Volumes with no recorded labels (seeded
		// directly into the map to model an already-up instance) are
		// assumed to carry the queried project.
		if lbl, ok := f.labels[k]; ok && lbl.project != composeProject {
			continue
		}
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

func (f *fakeArchiver) RestoreVolume(_ context.Context, composeProject, volume string, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.volumes == nil {
		f.volumes = map[string][]byte{}
	}
	// Restore is a REPLACE: the production archiver wipes the volume
	// before untar. Model that here by overwriting, not merging — a
	// straight assignment already replaces any prior body.
	f.volumes[volume] = body
	if f.labels == nil {
		f.labels = map[string]restoreLabels{}
	}
	f.labels[volume] = restoreLabels{project: composeProject, suffix: volumeSuffix(composeProject, volume)}
	return nil
}

func (f *fakeArchiver) VolumeStorageAvailableBytes(_ context.Context) (uint64, error) {
	if f.volStorageErr != nil {
		return 0, f.volStorageErr
	}
	if f.volStorageBytes == 0 {
		// Default: report a large value so disk preflight always
		// passes unless a test deliberately constrains it.
		return 1 << 50, nil // 1 PiB
	}
	return f.volStorageBytes, nil
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
	// round-trip via embedded state.json. We deliberately populate
	// every per-role map AND the DSO party so the round-trip test
	// (yellow Y11) can assert each one preserved verbatim.
	s.Ports = map[string]int{
		"app_user_ui":                 4485,
		"app_provider_ui":             4486,
		"sv_ui":                       4487,
		"participant_admin_app-user":  4488,
		"participant_ledger_app-user": 4489,
		"participant_json_app-user":   4490,
	}
	s.Credentials = map[string]registry.Credential{
		"app-user":     {Role: "app-user", User: "ledger-api-user", Audience: "aud", JWT: "eyJ.app-user-jwt"},
		"app-provider": {Role: "app-provider", User: "ledger-api-user", Audience: "aud", JWT: "eyJ.app-provider-jwt"},
		"sv":           {Role: "sv", User: "sv-user", Audience: "sv-aud", JWT: "eyJ.sigsv"},
	}
	// (DSO party is not yet a field on registry.State — once it
	// lands, add it to this seed and to the round-trip assertion
	// below.)
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestSnapshot_RoundTripsRegistryAndVolumes is the full happy path. It
// explicitly asserts the registry IS re-populated after restoring into
// a fresh registry root with no prior instance.
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
	// Assert EVERY port + credential round-trips byte-for-byte, not
	// just one — so a serializer regression that silently dropped half
	// the map can't pass.
	wantPorts := map[string]int{
		"app_user_ui":                 4485,
		"app_provider_ui":             4486,
		"sv_ui":                       4487,
		"participant_admin_app-user":  4488,
		"participant_ledger_app-user": 4489,
		"participant_json_app-user":   4490,
	}
	for k, v := range wantPorts {
		if got.Ports[k] != v {
			t.Errorf("port %q: got %d, want %d", k, got.Ports[k], v)
		}
	}
	wantJWTs := map[string]string{
		"app-user":     "eyJ.app-user-jwt",
		"app-provider": "eyJ.app-provider-jwt",
		"sv":           "eyJ.sigsv",
	}
	for role, wantJWT := range wantJWTs {
		c, ok := got.Credentials[role]
		if !ok {
			t.Errorf("credential %q: missing after round-trip", role)
			continue
		}
		if c.JWT != wantJWT {
			t.Errorf("credential %q JWT: got %q, want %q", role, c.JWT, wantJWT)
		}
		if c.Role != role {
			t.Errorf("credential %q Role field: got %q, want %q", role, c.Role, role)
		}
	}
}

// TestRestore_RejectsZipSlip is the security regression for the
// Zip Slip vulnerability. A malicious archive
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
// safety table.
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

// TestSnapshot_NotFoundIsUserError asserts a missing instance is a user error.
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

// TestRestore_SurfaceRegistryReadErrors covers the dropped-error half
// of the TOCTOU fix. A bare `existing, _ := registry.Read(name)` would
// swallow every error including permission denied and corrupt JSON,
// then proceed as if there were no existing instance. We assert the
// behaviour by seeding a CORRUPT state.json for the target name:
// restore must fail with ExitRuntimeFailure (read error surfaced)
// instead of silently overwriting.
func TestRestore_SurfaceRegistryReadErrors(t *testing.T) {
	regRoot := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", regRoot)
	seedSnapshotInstance(t, "demo")

	fa := &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": []byte("x")}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	// Corrupt the state.json (NOT delete — that would hit the
	// legitimate ErrNotFound path). The Read should return an
	// error other than ErrNotFound, which the new code surfaces.
	badPath := filepath.Join(regRoot, "demo", "state.json")
	if err := os.WriteFile(badPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt state: %v", err)
	}

	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}})
	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false)
	if code == localnet.ExitSuccess {
		t.Fatalf("RunRestore returned ExitSuccess despite corrupt registry state — read error was swallowed. stderr=%q", errBuf.String())
	}
	if code != localnet.ExitRuntimeFailure {
		t.Fatalf("RunRestore returned %d, want ExitRuntimeFailure; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "read existing registry state") {
		t.Errorf("stderr should explain the read failure, got %q", errBuf.String())
	}
}

// TestRestore_WarnsOnNameMismatch: when --name differs from the
// snapshot's embedded original name,
// restore must surface a warning. The restore still proceeds —
// renaming on restore is a supported workflow — but a stderr line
// must mention BOTH names so the user understands embedded log
// paths still reference the original.
func TestRestore_WarnsOnNameMismatch(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	fa := &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": []byte("x")}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	// Restore as a DIFFERENT name into a fresh registry.
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}})
	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "renamed", dest, false)
	if code != localnet.ExitSuccess {
		t.Fatalf("RunRestore (rename) failed: %d; stderr=%q", code, errBuf.String())
	}
	// Both names must appear in the warning so the user understands.
	if !strings.Contains(errBuf.String(), "demo") || !strings.Contains(errBuf.String(), "renamed") {
		t.Errorf("name-mismatch warning should mention BOTH names, got stderr=%q", errBuf.String())
	}
	if !strings.Contains(strings.ToLower(errBuf.String()), "warning") {
		t.Errorf("stderr should be labeled 'warning', got %q", errBuf.String())
	}
	// Restore must still succeed under the new name.
	if got, err := registry.Read("renamed"); err != nil || got.Name != "renamed" {
		t.Errorf("renamed instance not registered: %v / %+v", err, got)
	}
}

// TestAvailableDiskBytes_NonZero exercises the disk-preflight
// helper on the test temp dir. We only assert a non-zero result on
// the supported platforms — the function is allowed to return an
// error on filesystems where statfs is unavailable, and the
// caller treats errors as "preflight unavailable" (proceeds).
func TestAvailableDiskBytes_NonZero(t *testing.T) {
	got, err := availableDiskBytes(t.TempDir())
	if err != nil {
		t.Skipf("availableDiskBytes unavailable on this platform: %v", err)
	}
	if got == 0 {
		t.Errorf("availableDiskBytes returned 0 — should report some free space on test temp dir")
	}
}

// TestRewriteVolumeForTarget is the unit test for the helper.
// Pure-function table test; the integration test for the rename
// behaviour is TestRestore_CrossNameRewritesVolumes below.
func TestRewriteVolumeForTarget(t *testing.T) {
	tests := []struct {
		name, in, src, dst, want string
	}{
		{"same-name passthrough", "canton-pebble_postgres", "canton-pebble", "canton-pebble", "canton-pebble_postgres"},
		{"empty src passthrough", "canton-pebble_postgres", "", "canton-clone", "canton-pebble_postgres"},
		{"happy rewrite", "canton-pebble_postgres", "canton-pebble", "canton-clone", "canton-clone_postgres"},
		{"multi-segment suffix", "canton-pebble_domain-upgrade-dump", "canton-pebble", "canton-clone", "canton-clone_domain-upgrade-dump"},
		{"name without src prefix → unchanged", "foreign_volume", "canton-pebble", "canton-clone", "foreign_volume"},
		{"src prefix overlap with name → only exact prefix matches", "canton-pebble2_postgres", "canton-pebble", "canton-clone", "canton-pebble2_postgres"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteVolumeForTarget(tc.in, tc.src, tc.dst)
			if got != tc.want {
				t.Errorf("rewriteVolumeForTarget(%q, %q, %q) = %q, want %q",
					tc.in, tc.src, tc.dst, got, tc.want)
			}
		})
	}
}

// TestRestore_CrossNameRewritesVolumes is the integration test for the
// cross-name volume rewrite. Snapshot of "demo" (compose project
// "canton-demo") with two volumes, then restore --name demo-clone into
// a fresh registry.
// The restored docker volumes MUST land under "canton-demo-clone_*",
// not the original "canton-demo_*", or the cloned instance would
// share storage with the source and fail to `up` while it's running.
func TestRestore_CrossNameRewritesVolumes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	fa := &fakeArchiver{volumes: map[string][]byte{
		"canton-demo_postgres":            []byte("PG"),
		"canton-demo_domain-upgrade-dump": []byte("DUMP"),
	}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot code = %d, stderr=%q", code, errBuf.String())
	}

	// Fresh registry, fresh archiver target. Restore as "demo-clone".
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fa2 := &fakeArchiver{volumes: map[string][]byte{}}
	installFakeArchiver(t, fa2)
	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "demo-clone", dest, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code = %d, stderr=%q", code, errBuf.String())
	}

	// The volumes restored under the new compose-project prefix.
	wantVolumes := map[string]string{
		"canton-demo-clone_postgres":            "PG",
		"canton-demo-clone_domain-upgrade-dump": "DUMP",
	}
	for vol, wantBody := range wantVolumes {
		got, ok := fa2.volumes[vol]
		if !ok {
			t.Errorf("restored archiver missing %q; got keys %v", vol, keysOf(fa2.volumes))
			continue
		}
		if string(got) != wantBody {
			t.Errorf("volume %q body = %q, want %q", vol, got, wantBody)
		}
	}
	// And NOT under the source prefix — the whole point.
	for _, srcVol := range []string{"canton-demo_postgres", "canton-demo_domain-upgrade-dump"} {
		if _, leaked := fa2.volumes[srcVol]; leaked {
			t.Errorf("restored archiver still has source-named %q — cross-name rewrite did not apply", srcVol)
		}
	}

	// Registry should also reflect the new compose project so a
	// subsequent `up` brings up the right docker resources.
	clone, err := registry.Read("demo-clone")
	if err != nil {
		t.Fatalf("read demo-clone: %v", err)
	}
	if clone.ComposeProject != "canton-demo-clone" {
		t.Errorf("clone.ComposeProject = %q, want canton-demo-clone", clone.ComposeProject)
	}
	if clone.DockerNetwork != "demo-clone" {
		t.Errorf("clone.DockerNetwork = %q, want demo-clone", clone.DockerNetwork)
	}
	if clone.ContainerPrefix != "demo-clone-" {
		t.Errorf("clone.ContainerPrefix = %q, want demo-clone-", clone.ContainerPrefix)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSnapshot_WarnsWhenRunning asserts the consistency caveat is
// surfaced when snapshotting a running instance, and NOT when stopped.
func TestSnapshot_WarnsWhenRunning(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	// seed is stopped; flip to running.
	s, _ := registry.Read("demo")
	s.Status = registry.StatusRunning
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{"canton-demo_pg": []byte("x")}})

	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo",
		filepath.Join(t.TempDir(), "snap.tgz")); code != localnet.ExitSuccess {
		t.Fatalf("snapshot code = %d; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "running instance") {
		t.Errorf("expected a running-instance consistency warning, got: %q", errBuf.String())
	}
}

func TestSnapshot_NoWarnWhenStopped(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo") // stopped
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{"canton-demo_pg": []byte("x")}})

	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo",
		filepath.Join(t.TempDir(), "snap.tgz")); code != localnet.ExitSuccess {
		t.Fatalf("snapshot code = %d; stderr=%q", code, errBuf.String())
	}
	if strings.Contains(errBuf.String(), "running instance") {
		t.Errorf("did not expect a running-instance warning for a stopped instance: %q", errBuf.String())
	}
}

// TestRestore_ReappliesComposeLabels is the regression:
// restored volumes must carry the com.docker.compose.project /
// com.docker.compose.volume labels so ListVolumes (which filters on the
// project label) can find them again. Without the labels every later
// snapshot of the restored instance captures zero volumes.
func TestRestore_ReappliesComposeLabels(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	fa := &fakeArchiver{volumes: map[string][]byte{
		"canton-demo_postgres":   []byte("PG"),
		"canton-demo_canton-vol": []byte("CANTON"),
	}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fa2 := &fakeArchiver{volumes: map[string][]byte{}}
	installFakeArchiver(t, fa2)
	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code = %d; stderr=%q", code, errBuf.String())
	}

	// Every restored volume must have been stamped with the target
	// project label, and the per-volume suffix label.
	wantSuffix := map[string]string{
		"canton-demo_postgres":   "postgres",
		"canton-demo_canton-vol": "canton-vol",
	}
	for vol, suffix := range wantSuffix {
		lbl, ok := fa2.labels[vol]
		if !ok {
			t.Errorf("restored volume %q recorded no labels", vol)
			continue
		}
		if lbl.project != "canton-demo" {
			t.Errorf("volume %q project label = %q, want canton-demo", vol, lbl.project)
		}
		if lbl.suffix != suffix {
			t.Errorf("volume %q suffix label = %q, want %q", vol, lbl.suffix, suffix)
		}
	}
}

// TestSnapshot_RestoreSnapshot_SecondCaptureKeepsVolumes is the
// round-trip regression. The promised workflow is
// snapshot -> restore -> work -> snapshot again. Before the fix the
// SECOND snapshot found zero volumes (restored volumes lacked the
// project label ListVolumes filters on) and silently wrote an empty
// archive. Here the fake's ListVolumes honors the recorded labels, so
// a regression (dropping the relabel) would make this fail.
func TestSnapshot_RestoreSnapshot_SecondCaptureKeepsVolumes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	fa := &fakeArchiver{volumes: map[string][]byte{
		"canton-demo_postgres": []byte("PG-V1"),
	}}
	installFakeArchiver(t, fa)
	first := filepath.Join(t.TempDir(), "first.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", first); code != localnet.ExitSuccess {
		t.Fatalf("first snapshot failed: %d", code)
	}

	// Restore into a fresh registry + fresh archiver (a "new machine").
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fa2 := &fakeArchiver{volumes: map[string][]byte{}}
	installFakeArchiver(t, fa2)
	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", first, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code = %d; stderr=%q", code, errBuf.String())
	}

	// Now snapshot the restored instance AGAIN. It must still see the
	// volume (the relabel made it discoverable) and succeed.
	out.Reset()
	errBuf.Reset()
	second := filepath.Join(t.TempDir(), "second.tgz")
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo", second); code != localnet.ExitSuccess {
		t.Fatalf("second snapshot code = %d; stderr=%q", code, errBuf.String())
	}
	// The second archive must contain the volume, not be empty.
	meta := readHeaderOf(t, second)
	if len(meta.Volumes) != 1 || meta.Volumes[0].Name != "canton-demo_postgres" {
		t.Fatalf("second snapshot captured %d volume(s) %+v — expected the restored postgres volume; "+
			"restored volume was not discoverable (lost its compose project label)",
			len(meta.Volumes), meta.Volumes)
	}
}

// TestSnapshot_RefusesEmptyVolumeList pins that
// an empty volume list must be a loud refusal, not a silent
// 0-volume success archive.
func TestSnapshot_RefusesEmptyVolumeList(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}})

	dest := filepath.Join(t.TempDir(), "snap.tgz")
	var out, errBuf bytes.Buffer
	code := RunSnapshot(context.Background(), &out, &errBuf, "demo", dest)
	if code != localnet.ExitUserError {
		t.Fatalf("empty-volume snapshot code = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "No docker volumes") &&
		!strings.Contains(errBuf.String(), "empty snapshot") {
		t.Errorf("stderr should explain the empty-volume refusal, got %q", errBuf.String())
	}
	// And it must NOT have written an archive.
	if _, err := os.Stat(dest); err == nil {
		t.Errorf("empty-volume snapshot wrote an archive at %s — should refuse before writing", dest)
	}
}

// TestRestore_RefusesWhenDockerStorageTooSmall is the regression for
// the disk preflight must measure DOCKER's volume storage
// (VolumeStorageAvailableBytes), not the host filesystem holding the
// archive. We give the archiver a tiny Docker-storage figure; the
// restore must refuse even though the host temp dir (where the archive
// lives) has plenty of room.
func TestRestore_RefusesWhenDockerStorageTooSmall(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	// A volume big enough that 20% margin matters, then constrain
	// Docker storage below it on restore.
	body := bytes.Repeat([]byte("A"), 4096)
	fa := &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": body}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	// Report only 1 KiB of Docker volume storage — far below the
	// ~4 KiB volume. The host fs holding `dest` is huge, so a host-fs
	// gate would pass; only the Docker-storage gate refuses.
	installFakeArchiver(t, &fakeArchiver{volumes: map[string][]byte{}, volStorageBytes: 1024})
	var out, errBuf bytes.Buffer
	code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false)
	if code != localnet.ExitUserError {
		t.Fatalf("restore code = %d, want ExitUserError (Docker storage too small); stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "Docker's volume storage") {
		t.Errorf("refusal should cite Docker's volume storage, got %q", errBuf.String())
	}
}

// TestRestore_DockerStorageProbeFailureFallsBackToHost asserts the
// graceful degradation path: when the Docker probe
// errors (old docker, missing image), the preflight falls back to the
// host filesystem rather than blocking restore. With a huge host fs the
// restore must still succeed.
func TestRestore_DockerStorageProbeFailureFallsBackToHost(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	fa := &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": []byte("x")}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFakeArchiver(t, &fakeArchiver{
		volumes:       map[string][]byte{},
		volStorageErr: errors.New("docker probe boom"),
	})
	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false); code != localnet.ExitSuccess {
		t.Fatalf("restore should fall back to host statfs and succeed, code = %d; stderr=%q", code, errBuf.String())
	}
}

// TestRestore_ReplacesStaleVolumeContents is the regression.
// Restore must be a true REPLACE, not a tar-merge onto stale
// contents. The fake's RestoreVolume overwrites the body (mirroring the
// production `find -delete && tar xf`), so a leftover key from a prior
// restore must NOT survive into the new body.
func TestRestore_ReplacesStaleVolumeContents(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedSnapshotInstance(t, "demo")
	fa := &fakeArchiver{volumes: map[string][]byte{"canton-demo_postgres": []byte("SNAPSHOT-CONTENT")}}
	installFakeArchiver(t, fa)
	dest := filepath.Join(t.TempDir(), "snap.tgz")
	if code := RunSnapshot(context.Background(), io.Discard, io.Discard, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot failed: %d", code)
	}

	// Restore over an archiver whose target volume ALREADY holds stale
	// bytes (models a stopped-but-extant instance / failed bring-up).
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	stale := &fakeArchiver{volumes: map[string][]byte{
		"canton-demo_postgres": []byte("STALE-PRE-EXISTING-WAL"),
	}}
	installFakeArchiver(t, stale)
	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code = %d; stderr=%q", code, errBuf.String())
	}
	got := stale.volumes["canton-demo_postgres"]
	if string(got) != "SNAPSHOT-CONTENT" {
		t.Errorf("restored volume body = %q, want exactly the snapshot content (stale bytes must be wiped, not merged)", got)
	}
}

// TestVolumeSuffix unit-tests the Compose-volume-label derivation used
// when re-stamping restored volumes.
func TestVolumeSuffix(t *testing.T) {
	cases := []struct{ project, volume, want string }{
		{"canton-demo", "canton-demo_postgres", "postgres"},
		{"canton-demo", "canton-demo_domain-upgrade-dump", "domain-upgrade-dump"},
		{"canton-demo", "foreign_volume", ""},        // no project prefix
		{"canton-demo", "canton-demo", ""},           // prefix without "_<suffix>"
		{"canton-demo2", "canton-demo_postgres", ""}, // adjacent project, exact prefix only
	}
	for _, c := range cases {
		if got := volumeSuffix(c.project, c.volume); got != c.want {
			t.Errorf("volumeSuffix(%q, %q) = %q, want %q", c.project, c.volume, got, c.want)
		}
	}
}

// TestRunningSnapshotWarning pins the shared running-instance caveat
// wording. Both surfaces consume this: the CLI prints it
// to stderr, the Web UI handler echoes it in X-Snapshot-Warning. The
// instance name must appear so the `pause` hint is copy-pasteable.
func TestRunningSnapshotWarning(t *testing.T) {
	got := RunningSnapshotWarning("pebble")
	if !strings.Contains(got, "crash-consistent") {
		t.Errorf("warning missing crash-consistency note: %q", got)
	}
	if !strings.Contains(got, "localnet pause --name pebble") {
		t.Errorf("warning should embed a copy-pasteable pause hint, got %q", got)
	}
}

// TestParseDfAvailableKB covers the df parser feeding the
// Docker-storage probe.
func TestParseDfAvailableKB(t *testing.T) {
	const out = "Filesystem           1024-blocks      Used Available Capacity Mounted on\n" +
		"overlay              61202244   12345678  45000000      22% /probe\n"
	got, err := parseDfAvailableKB(out)
	if err != nil {
		t.Fatalf("parseDfAvailableKB: %v", err)
	}
	if want := uint64(45000000) * 1024; got != want {
		t.Errorf("parseDfAvailableKB = %d, want %d", got, want)
	}

	if _, err := parseDfAvailableKB("garbage with no numbers"); err == nil {
		t.Error("parseDfAvailableKB should error on unparseable output")
	}
}

// readHeaderOf opens a written snapshot archive and returns its
// snapshot.json header — used to assert volume counts post-capture.
func readHeaderOf(t *testing.T, archivePath string) *types.Snapshot {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open %s: %v", archivePath, err)
	}
	defer func() { _ = f.Close() }()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip %s: %v", archivePath, err)
	}
	defer func() { _ = gzr.Close() }()
	meta, err := readSnapshotHeader(tar.NewReader(gzr))
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	return meta
}
