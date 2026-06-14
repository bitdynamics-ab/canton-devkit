package handlers

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/snapshot"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// The snapshot handler is exercised with snapshot.FakeArchiver — the
// docker-free in-memory pgArchiver the snapshot package exposes for
// cross-package tests — so these handler tests run without a daemon.

// snapshotMux mounts only the snapshots handlers — the other Mount*
// calls would require docker / hub wiring that's irrelevant to these
// tests. Same isolation pattern instances_test.go uses for its own
// surface.
func snapshotMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountSnapshots(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestSnapshot_RejectsInvalidName pins the name-validation gate at
// the handler boundary. The instance-name validator (registry.ValidateName)
// rejects anything outside the DNS-label set, and the handler must
// surface a 400 with the standard error shape before touching disk
// or docker.
func TestSnapshot_RejectsInvalidName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := snapshotMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/..%2Fetc%2Fpasswd/snapshot",
		"application/octet-stream", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestSnapshot_NotFoundForUnknownInstance pins the "registry-first"
// check: snapshot returns 404 with INSTANCE_NOT_FOUND when the named
// instance isn't registered, without touching docker. This is the
// shape the frontend's error toast looks for.
func TestSnapshot_NotFoundForUnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := snapshotMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/ghost/snapshot",
		"application/octet-stream", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "INSTANCE_NOT_FOUND") {
		t.Fatalf("want body to contain INSTANCE_NOT_FOUND, got %s", body)
	}
}

// TestRestore_RejectsEmptyMultipart pins that a malformed/empty body
// produces a clean 400 instead of a panic or 500. The two failure
// paths we care about: no `file` field, and a `file` field whose
// content is empty.
func TestRestore_RejectsEmptyMultipart(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := snapshotMux(t)

	// Build a multipart body with `name` but no `file`. The server
	// must respond 400 with the standard error shape.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "pebble")
	_ = mw.Close()
	resp, err := http.Post(srv.URL+"/api/instances/restore",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestRestore_RejectsInvalidName guards the same instance-name gate
// for the restore path. A path-traversal-shaped `name` field must
// be rejected at the handler before the tar parser sees the body —
// the body could be perfectly valid and we still wouldn't accept it
// because the target name is unsafe.
func TestRestore_RejectsInvalidName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := snapshotMux(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "../etc/passwd")
	fw, _ := mw.CreateFormFile("file", "x.tgz")
	_, _ = fw.Write([]byte("not really a tar"))
	_ = mw.Close()
	resp, err := http.Post(srv.URL+"/api/instances/restore",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestRestore_RejectsCorruptArchive pins that a `name`-valid request
// with a `file` payload that ISN'T a real snapshot fails cleanly at
// the orchestrator boundary. The CLI returns ExitUserError for
// header-validation failure; the handler must map that to 400.
//
// We don't supply a real instance — the orchestrator validates the
// archive before reading the registry, so a missing instance is
// fine for this test.
func TestRestore_RejectsCorruptArchive(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := snapshotMux(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "pebble")
	fw, _ := mw.CreateFormFile("file", "bogus.tgz")
	// Random bytes — not gzip, not tar.
	_, _ = fw.Write(bytes.Repeat([]byte{0x41}, 64))
	_ = mw.Close()
	resp, err := http.Post(srv.URL+"/api/instances/restore",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 (corrupt archive maps to user error), got %d",
			resp.StatusCode)
	}
}

// TestSnapshot_PathValueWiring guards the Go 1.22 mux pattern. If
// MountSnapshots accidentally drops the `{name}` segment, requests
// would 404 or pass an empty name through. Existing instance check
// at the validate step would still 400, but the diagnostic value
// of this test is asserting the path parses cleanly.
func TestSnapshot_PathValueWiring(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := snapshotMux(t)
	// Use a registry helper that lets us assert the handler reached
	// registry.Read with the correct name — easiest signal: a
	// well-formed name that returns 404 (instance not registered)
	// rather than 400 (name invalid).
	resp, err := http.Post(srv.URL+"/api/instances/well-formed/snapshot",
		"application/octet-stream", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 (name parsed, instance not found), got %d",
			resp.StatusCode)
	}
	// Sanity: registry should never have been written to. If a
	// future regression accidentally wrote an entry on GET, this
	// catches it.
	_, err = registry.Read("well-formed")
	if err == nil {
		t.Fatalf("registry unexpectedly contains 'well-formed'")
	}
}

// seedSnapshotState registers an instance with the given status so the
// snapshot success path can run against the stub archiver.
func seedSnapshotState(t *testing.T, name string, status registry.Status) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = status
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestSnapshot_RunningInstanceIsConsistentNoWarning: the UI snapshot of
// a running instance succeeds and carries NO consistency warning header —
// RunSnapshot quiesces the node containers for the dump (same as the CLI),
// so the capture is application-consistent and there is nothing to warn
// about. (Was the X-Snapshot-Warning parity test back when the dump ran
// live.)
func TestSnapshot_RunningInstanceIsConsistentNoWarning(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fa := &snapshot.FakeArchiver{Dump: []byte("-- pg_dumpall\n"), DBCount: 3}
	restore := snapshot.SetArchiverForTest(fa)
	defer restore()
	seedSnapshotState(t, "run", registry.StatusRunning)

	srv := snapshotMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/run/snapshot",
		"application/octet-stream", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d; body=%s", resp.StatusCode, body)
	}
	if warn := resp.Header.Get("X-Snapshot-Warning"); warn != "" {
		t.Errorf("a quiesced snapshot must NOT set X-Snapshot-Warning, got %q", warn)
	}
	if !fa.Quiesced || !fa.Resumed {
		t.Errorf("UI snapshot must quiesce+resume writers: quiesced=%v resumed=%v", fa.Quiesced, fa.Resumed)
	}
}

// TestSnapshot_StoppedInstanceRefused: a database snapshot reads from a
// live Postgres, so a stopped instance is refused rather than producing
// an empty/garbage archive.
func TestSnapshot_StoppedInstanceRefused(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	restore := snapshot.SetArchiverForTest(&snapshot.FakeArchiver{Dump: []byte("x")})
	defer restore()
	seedSnapshotState(t, "stp", registry.StatusStopped)

	srv := snapshotMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/stp/snapshot",
		"application/octet-stream", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("snapshot of a stopped instance must be refused with 400, got %d; body=%s",
			resp.StatusCode, body)
	}
}
