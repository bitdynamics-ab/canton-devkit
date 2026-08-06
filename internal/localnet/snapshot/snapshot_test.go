package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// ---- helpers ----

func installFake(t *testing.T, fa *FakeArchiver) {
	t.Helper()
	restore := SetArchiverForTest(fa)
	t.Cleanup(restore)
}

// seedInstance writes a registry record with a fully-populated set of
// ports + credentials, so the round-trip can assert each survives the
// embedded state.json.
func seedInstance(t *testing.T, name, version string, status registry.Status) {
	t.Helper()
	s := registry.NewState(name, version)
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = status
	s.Ports = map[string]int{
		"app_user_ui":                 4485,
		"participant_ledger_app-user": 4489,
	}
	s.Credentials = map[string]registry.Credential{
		"app-user": {Role: "app-user", User: "ledger-api-user", Audience: "aud", JWT: "eyJ.app-user-jwt"},
		"sv":       {Role: "sv", User: "sv-user", Audience: "sv-aud", JWT: "eyJ.sigsv"},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func sha256Of(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// validHeader builds a schema-2 header whose Database descriptor matches
// dump (correct size + SHA).
func validHeader(name, version string, dump []byte) types.Snapshot {
	return types.Snapshot{
		SchemaVersion: snapshotSchemaVersion,
		Instance:      name,
		SpliceVersion: version,
		CreatedAt:     "2026-06-14T00:00:00Z",
		DevKitVersion: "dev",
		Database: &types.SnapshotDatabase{
			Engine:        "postgresql",
			PostgresImage: "postgres:14",
			User:          "cnadmin",
			VolumeSuffix:  "postgres",
			DatabaseCount: 3,
			SizeBytes:     int64(len(dump)),
			ContentSHA:    sha256Of(dump),
		},
	}
}

// writeArchive crafts an archive with the exact (header, state, dump)
// given — used to build malformed archives the happy path can't.
func writeArchive(t *testing.T, dst string, header types.Snapshot, state *registry.State, dump []byte) {
	t.Helper()
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write := func(name string, body []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(body)), Mode: 0o600}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	hb, _ := json.MarshalIndent(header, "", "  ")
	write(archiveHeaderName, hb)
	sb, _ := json.MarshalIndent(state, "", "  ")
	write(archiveStateName, sb)
	write(archiveDumpName, dump)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// stateFor returns a registry.State matching seedInstance, for embedding
// in a hand-crafted archive.
func stateFor(t *testing.T, name, version string) *registry.State {
	t.Helper()
	s := registry.NewState(name, version)
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.Status = registry.StatusStopped
	return s
}

// ---- snapshot ----

func TestSnapshot_RoundTrip(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusRunning)

	dump := []byte("-- pg_dumpall --clean\nCREATE DATABASE x;\n")
	faSnap := &FakeArchiver{Dump: dump, DBCount: 3}
	installFake(t, faSnap)

	dest := filepath.Join(t.TempDir(), "snap.tgz")
	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo", dest); code != localnet.ExitSuccess {
		t.Fatalf("snapshot code=%d stderr=%q", code, errBuf.String())
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("archive not written: %v", err)
	}
	// The snapshot must quiesce writers for the dump (no-writes single-step
	// backup) and resume them after.
	if !faSnap.Quiesced || !faSnap.Resumed {
		t.Errorf("snapshot must quiesce+resume writers: quiesced=%v resumed=%v", faSnap.Quiesced, faSnap.Resumed)
	}
	// The operator is warned about the brief pause before it happens, so the
	// downtime isn't a surprise (CLI parity with the Web UI's "Snapshotting…").
	if !strings.Contains(out.String(), "pauses briefly") {
		t.Errorf("snapshot must warn about the brief pause before quiescing; out=%q", out.String())
	}

	// Restore into a FRESH registry root. Restore requires the target
	// instance to already exist (it was `up`, so its Compose volume
	// exists) — so seed a STOPPED `demo` before restoring.
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusStopped)
	fa2 := &FakeArchiver{}
	installFake(t, fa2)
	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dest, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code=%d stderr=%q", code, errBuf.String())
	}

	// The dump streamed into RestoreInto byte-for-byte, into the right volume.
	if !bytes.Equal(fa2.Restored, dump) {
		t.Errorf("restored dump mismatch: got %q want %q", fa2.Restored, dump)
	}
	if fa2.RestoredVolume != "canton-demo_postgres" {
		t.Errorf("restore volume = %q, want canton-demo_postgres", fa2.RestoredVolume)
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
		t.Errorf("restored Status=%q want stopped", got.Status)
	}
	if got.Ports["participant_ledger_app-user"] != 4489 {
		t.Errorf("port not preserved: %+v", got.Ports)
	}
	if c, ok := got.Credentials["app-user"]; !ok || c.JWT != "eyJ.app-user-jwt" {
		t.Errorf("credential not preserved: %+v", got.Credentials)
	}
}

func TestSnapshot_RefusesStoppedInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusStopped)
	installFake(t, &FakeArchiver{})

	var out, errBuf bytes.Buffer
	code := RunSnapshot(context.Background(), &out, &errBuf, "demo", filepath.Join(t.TempDir(), "s.tgz"))
	if code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError", code)
	}
	if !strings.Contains(errBuf.String(), "not running") {
		t.Errorf("stderr should explain the instance must be running, got %q", errBuf.String())
	}
}

func TestSnapshot_NotFoundIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFake(t, &FakeArchiver{})
	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "ghost", filepath.Join(t.TempDir(), "s.tgz")); code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError", code)
	}
}

// TestSnapshot_ResumesWritersOnDumpFailure pins that a failed dump still
// unpauses the node containers — otherwise a snapshot error would leave
// the instance frozen.
func TestSnapshot_ResumesWritersOnDumpFailure(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusRunning)
	fa := &FakeArchiver{DumpErr: errors.New("pg_dumpall boom")}
	installFake(t, fa)

	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo", filepath.Join(t.TempDir(), "s.tgz")); code == localnet.ExitSuccess {
		t.Fatal("expected failure on dump error")
	}
	if !fa.Quiesced || !fa.Resumed {
		t.Errorf("writers must be resumed even when the dump fails: quiesced=%v resumed=%v", fa.Quiesced, fa.Resumed)
	}
}

// ---- restore refusals ----

func TestRestore_RefusesVolumeFormatArchive(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFake(t, &FakeArchiver{})
	dst := filepath.Join(t.TempDir(), "v1.tgz")
	h := types.Snapshot{SchemaVersion: 1, Instance: "demo", SpliceVersion: "0.6.4"} // Database == nil
	writeArchive(t, dst, h, stateFor(t, "demo", "0.6.4"), []byte("ignored"))

	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dst, false); code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError", code)
	}
	if !strings.Contains(errBuf.String(), "predates the database-dump format") {
		t.Errorf("stderr=%q", errBuf.String())
	}
}

func TestRestore_RefusesNewerSchema(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	installFake(t, &FakeArchiver{})
	dst := filepath.Join(t.TempDir(), "newer.tgz")
	dump := []byte("dump")
	h := validHeader("demo", "0.6.4", dump)
	h.SchemaVersion = snapshotSchemaVersion + 1
	writeArchive(t, dst, h, stateFor(t, "demo", "0.6.4"), dump)

	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dst, false); code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError", code)
	}
	if !strings.Contains(errBuf.String(), "newer than") {
		t.Errorf("stderr=%q", errBuf.String())
	}
}

func TestRestore_RefusesRunningInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusRunning)
	installFake(t, &FakeArchiver{})
	dst := filepath.Join(t.TempDir(), "a.tgz")
	dump := []byte("dump")
	writeArchive(t, dst, validHeader("demo", "0.6.4", dump), stateFor(t, "demo", "0.6.4"), dump)

	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dst, false); code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError", code)
	}
	if !strings.Contains(errBuf.String(), "running") {
		t.Errorf("stderr=%q", errBuf.String())
	}
}

func TestRestore_RefusesUnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir()) // empty registry — no instance
	installFake(t, &FakeArchiver{})
	dst := filepath.Join(t.TempDir(), "a.tgz")
	dump := []byte("dump")
	writeArchive(t, dst, validHeader("ghost", "0.6.4", dump), stateFor(t, "ghost", "0.6.4"), dump)

	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "ghost", dst, false); code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError (instance never up)", code)
	}
	if !strings.Contains(errBuf.String(), "not found") || !strings.Contains(errBuf.String(), "localnet up") {
		t.Errorf("expected 'not found / run localnet up first', stderr=%q", errBuf.String())
	}
}

func TestRestore_RefusesSpliceVersionMismatch(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.3", registry.StatusStopped) // existing is 0.6.3
	installFake(t, &FakeArchiver{})
	dst := filepath.Join(t.TempDir(), "a.tgz")
	dump := []byte("dump")
	writeArchive(t, dst, validHeader("demo", "0.6.4", dump), stateFor(t, "demo", "0.6.4"), dump) // snapshot is 0.6.4

	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dst, false); code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError (mismatch without --force)", code)
	}
	// --force overrides.
	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dst, true); code != localnet.ExitSuccess {
		t.Fatalf("force restore code=%d stderr=%q", code, errBuf.String())
	}
}

func TestRestore_RefusesIfContainersRunning(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusStopped)
	installFake(t, &FakeArchiver{Running: true}) // docker still shows a live container
	dst := filepath.Join(t.TempDir(), "a.tgz")
	dump := []byte("dump")
	writeArchive(t, dst, validHeader("demo", "0.6.4", dump), stateFor(t, "demo", "0.6.4"), dump)

	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dst, false); code != localnet.ExitUserError {
		t.Fatalf("code=%d want ExitUserError", code)
	}
	if !strings.Contains(errBuf.String(), "running containers") {
		t.Errorf("stderr=%q", errBuf.String())
	}
}

func TestRestore_VerifiesContentSHA(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4", registry.StatusStopped)
	installFake(t, &FakeArchiver{})
	dst := filepath.Join(t.TempDir(), "tamper.tgz")
	dump := []byte("the real dump bytes")
	h := validHeader("demo", "0.6.4", dump)
	h.Database.ContentSHA = "sha256:deadbeef" // recorded SHA doesn't match the dump
	writeArchive(t, dst, h, stateFor(t, "demo", "0.6.4"), dump)

	var out, errBuf bytes.Buffer
	if code := RunRestore(context.Background(), &out, &errBuf, "demo", dst, false); code != localnet.ExitRuntimeFailure {
		t.Fatalf("code=%d want ExitRuntimeFailure (SHA mismatch)", code)
	}
	if !strings.Contains(errBuf.String(), "SHA mismatch") {
		t.Errorf("stderr=%q", errBuf.String())
	}
}

func TestRestore_CrossNameRewritesProject(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	// Snapshot "demo" running, restore as "clone".
	seedInstance(t, "demo", "0.6.4", registry.StatusRunning)
	dump := []byte("-- dump\n")
	installFake(t, &FakeArchiver{Dump: dump, DBCount: 2})
	dst := filepath.Join(t.TempDir(), "demo.tgz")
	var out, errBuf bytes.Buffer
	if code := RunSnapshot(context.Background(), &out, &errBuf, "demo", dst); code != localnet.ExitSuccess {
		t.Fatalf("snapshot code=%d stderr=%q", code, errBuf.String())
	}

	// Cross-name restore still requires the TARGET name to already exist
	// (it was `up`, so `canton-clone_postgres` exists). Seed a stopped
	// `clone`; the embedded snapshot name stays `demo`, so the rename
	// warning still fires below.
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "clone", "0.6.4", registry.StatusStopped)
	fa2 := &FakeArchiver{}
	installFake(t, fa2)
	out.Reset()
	errBuf.Reset()
	if code := RunRestore(context.Background(), &out, &errBuf, "clone", dst, false); code != localnet.ExitSuccess {
		t.Fatalf("restore code=%d stderr=%q", code, errBuf.String())
	}
	if fa2.RestoredVolume != "canton-clone_postgres" {
		t.Errorf("cross-name restore volume = %q, want canton-clone_postgres", fa2.RestoredVolume)
	}
	if !strings.Contains(errBuf.String(), "original instance name was \"demo\"") {
		t.Errorf("cross-name restore should warn, stderr=%q", errBuf.String())
	}
	got, err := registry.Read("clone")
	if err != nil {
		t.Fatalf("clone not registered: %v", err)
	}
	if got.ComposeProject != "canton-clone" || got.ContainerPrefix != "clone-" {
		t.Errorf("cross-name fields not rewritten: %+v", got)
	}
}

// ---- units ----

func TestNonIgnorableRestoreErrors(t *testing.T) {
	stderr := strings.Join([]string{
		`ERROR:  role "cnadmin" already exists`,
		`ERROR:  current user cannot be dropped`,
		`psql:dump.sql:42: ERROR:  relation "foo" already exists`,
		`some informational line`,
		`FATAL:  out of disk`,
	}, "\n")
	bad := nonIgnorableRestoreErrors(stderr, "cnadmin")
	if len(bad) != 2 {
		t.Fatalf("got %d non-ignorable errors, want 2: %v", len(bad), bad)
	}
	if !strings.Contains(bad[0], "relation") || !strings.Contains(bad[1], "out of disk") {
		t.Errorf("wrong errors surfaced: %v", bad)
	}
}

func TestValidArchiveDumpPath(t *testing.T) {
	good := []string{archiveDumpName}
	bad := []string{"", "database/../etc/passwd", "database/dumpall.sql/x", "volumes/x.tar", "dumpall.sql", "./database/dumpall.sql"}
	for _, g := range good {
		if !validArchiveDumpPath(g) {
			t.Errorf("validArchiveDumpPath(%q) = false, want true", g)
		}
	}
	for _, b := range bad {
		if validArchiveDumpPath(b) {
			t.Errorf("validArchiveDumpPath(%q) = true, want false", b)
		}
	}
}

func TestCappedWriter_EnforcesCeiling(t *testing.T) {
	var sink bytes.Buffer
	cw := newCappedWriter(&sink, 4)
	if _, err := cw.Write([]byte("abc")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := cw.Write([]byte("de")); err == nil {
		t.Fatal("write past ceiling should error")
	}
	if !cw.exceeded {
		t.Error("exceeded flag not set")
	}
}

func TestAvailableDiskBytes_NonZero(t *testing.T) {
	avail, err := availableDiskBytes(t.TempDir())
	if err != nil {
		t.Skipf("statfs unsupported: %v", err)
	}
	if avail == 0 {
		t.Error("availableDiskBytes returned 0 for a real temp dir")
	}
}

func TestParseDfAvailableKB(t *testing.T) {
	out := "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/x 100 40 60 40% /probe\n"
	got, err := parseDfAvailableKB(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != 60*1024 {
		t.Errorf("got %d, want %d", got, 60*1024)
	}
}
