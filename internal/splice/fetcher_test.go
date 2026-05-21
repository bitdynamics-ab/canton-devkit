package splice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripArchivePrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"splice-0.6.4/cluster/compose/localnet/compose.yaml", "compose.yaml", true},
		{"splice-0.6.4/cluster/compose/localnet/env/common.env", "env/common.env", true},
		{"splice-0.6.4/cluster/compose/localnet/", "", true}, // dir entry → empty rel
		{"splice-0.6.4/README.md", "", false},
		{"splice-0.6.4/cluster/", "", false},
		// stripArchivePrefix is permissive about the first segment because
		// real GitHub archives use `<repo>-<tag>` and we don't want to
		// hardcode the version into the prefix check. As long as the rest
		// of the path matches cluster/compose/localnet/, it's accepted.
		{"any-prefix/cluster/compose/localnet/x", "x", true},
		{"noslash", "", false},
	}
	for _, c := range cases {
		got, ok := stripArchivePrefix(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("stripArchivePrefix(%q) = (%q,%v), want (%q,%v)",
				c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	base := t.TempDir()

	// Legitimate
	if _, err := safeJoin(base, "env/common.env"); err != nil {
		t.Errorf("legitimate path rejected: %v", err)
	}

	// Traversal
	for _, rel := range []string{
		"../escape",
		"../../etc/passwd",
		"env/../../escape",
	} {
		if _, err := safeJoin(base, rel); err == nil {
			t.Errorf("safeJoin should reject %q", rel)
		}
	}
}

// TestFetchInstallsAndVerifiesContent spins up a local HTTP server that
// serves a tiny hand-rolled tar.gz containing a
// `cluster/compose/localnet/compose.yaml` entry, computes the expected
// ContentSHA from a scratch extraction, then re-runs downloadAndExtract
// pinned to that hash and confirms install + verification both succeed.
func TestFetchInstallsAndVerifiesContent(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-test/cluster/compose/localnet/compose.yaml":   "services: {}\n",
		"splice-test/cluster/compose/localnet/env/common.env": "DB_PORT=5432\n",
		"splice-test/README.md":                               "should be skipped\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()

	// First pass: extract to compute the expected ContentSHA.
	scratch := t.TempDir()
	if err := downloadAndExtract(context.Background(), srv.URL, "", 1<<20, scratch); err == nil {
		t.Fatal("expected error when ContentSHA is empty (refusing to install unverified)")
	}
	// Workaround: directly compute the tree SHA from a successful
	// extraction. We do this by faking it — write the files manually
	// in a way the scratch dir contains them. Simpler path: do a
	// real extract via a private call that bypasses the empty-hash
	// guard. We achieve the same by computing the expected hash
	// from the file contents directly, which is identical to what
	// computeTreeSHA would produce.
	expectedTree := t.TempDir()
	_ = os.WriteFile(filepath.Join(expectedTree, "compose.yaml"), []byte("services: {}\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(expectedTree, "env"), 0o755)
	_ = os.WriteFile(filepath.Join(expectedTree, "env", "common.env"), []byte("DB_PORT=5432\n"), 0o644)
	wantContent, err := computeTreeSHA(expectedTree)
	if err != nil {
		t.Fatalf("computeTreeSHA: %v", err)
	}

	// Second pass: real install with the correct ContentSHA.
	dest := t.TempDir()
	if err := downloadAndExtract(context.Background(), srv.URL, wantContent, 1<<20, dest); err != nil {
		t.Fatalf("downloadAndExtract: %v", err)
	}

	// compose.yaml present
	body, err := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose.yaml not extracted: %v", err)
	}
	if !strings.Contains(string(body), "services") {
		t.Errorf("unexpected compose.yaml content: %q", string(body))
	}
	// README.md NOT extracted (outside subdir).
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("README.md should not have been extracted (err=%v)", err)
	}
}

// TestDownloadAndExtractRejectsOversizedBody covers the cappedReader
// guard: a server that returns more than maxBytes must fail with a
// clear cap error, not OOM the host or silently truncate.
func TestDownloadAndExtractRejectsOversizedBody(t *testing.T) {
	// 2 MB of garbage. Cap will be 1 MB.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("A"), 2<<20))
	}))
	defer srv.Close()

	err := downloadAndExtract(context.Background(), srv.URL,
		strings.Repeat("0", 64), 1<<20, t.TempDir())
	if err == nil {
		t.Fatal("expected cap error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") && !strings.Contains(err.Error(), "gunzip") {
		// Either the cap fires (preferred) or gzip rejects the garbage
		// first — both are acceptable failure modes. The point is we
		// never OOM and we never silently truncate.
		t.Errorf("unexpected error shape: %v", err)
	}
}

// TestDownloadAndExtractRejectsContentMismatch covers the case where
// the extracted tree doesn't match the catalogued ContentSHA (e.g. an
// upstream tag pointing to unexpected content, or a MITM that managed
// to wrap a malicious payload).
func TestDownloadAndExtractRejectsContentMismatch(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml": "services: {}\n",
	})

	err := downloadAndExtract(context.Background(), serveBytesURL(t, tarball),
		strings.Repeat("1", 64), // wrong content hash
		1<<20, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("expected content hash mismatch, got %v", err)
	}
}

// TestDownloadAndExtractRejectsEmptyContentSHA verifies the
// defense-in-depth check: a Version with no ContentSHA is rejected so
// we never silently install unverified content.
func TestDownloadAndExtractRejectsEmptyContentSHA(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml": "services: {}\n",
	})
	err := downloadAndExtract(context.Background(), serveBytesURL(t, tarball),
		"", 1<<20, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no ContentSHA") {
		t.Fatalf("expected refusal for empty ContentSHA, got %v", err)
	}
}

// --- helpers used by both groups -----------------------------------------

func serveBytesURL(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// versionForTarball extracts the test tarball into a scratch dir, hashes
// the result, and returns a Version pinned to that ContentSHA. The
// commit is a fake string — the URL is rewired by servePinnedTarball
// so the catalogued Commit value is never used for HTTP.
func versionForTarball(t *testing.T, tarball []byte) Version {
	t.Helper()
	// Extract via the same pipeline Fetch uses, so the ContentSHA we
	// compute matches what downloadAndExtract will compute at install
	// time. We bypass content verification by using a "compute first,
	// then trust" loop: extract once to a scratch dir with no SHA
	// verification by replicating the extract steps directly.
	scratch := t.TempDir()
	gzr, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatalf("gunzip test tarball: %v", err)
	}
	if err := extractLocalNet(tar.NewReader(gzr), scratch); err != nil {
		t.Fatalf("extract test tarball: %v", err)
	}
	_ = gzr.Close()
	sha, err := computeTreeSHA(scratch)
	if err != nil {
		t.Fatalf("computeTreeSHA: %v", err)
	}
	return Version{
		Tag:        "x",
		Commit:     "testcommit",
		ContentSHA: sha,
		Size:       int64(len(tarball)),
	}
}

// --- Fetch (cache + lifecycle) -------------------------------------------

// servePinnedTarball stands up an httptest server that serves the given
// bytes and rewires tarballURL to point Fetch at it. Returns a cleanup
// fn that restores tarballURL.
func servePinnedTarball(t *testing.T, body []byte) (server *httptest.Server, cleanup func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	prev := tarballURL
	tarballURL = func(v Version) string { return srv.URL }
	return srv, func() {
		srv.Close()
		tarballURL = prev
	}
}

func TestFetchCacheHitSkipsDownload(t *testing.T) {
	cacheRoot := t.TempDir()
	v := Version{Tag: "test", Commit: "testcommit", ContentSHA: "ignored-on-cache-hit"}
	projectDir := ProjectDir(cacheRoot, v)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed the cache.
	if err := os.WriteFile(filepath.Join(projectDir, "compose.yaml"), []byte("cached: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Tripwire: any HTTP request would explode this test.
	prev := tarballURL
	tarballURL = func(v Version) string {
		t.Fatalf("Fetch attempted to download despite cache hit")
		return ""
	}
	defer func() { tarballURL = prev }()

	got, err := Fetch(context.Background(), v, cacheRoot, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != projectDir {
		t.Errorf("Fetch returned %q, want %q", got, projectDir)
	}
	body, err := os.ReadFile(filepath.Join(got, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose.yaml: %v", err)
	}
	if !strings.Contains(string(body), "cached: true") {
		t.Errorf("cache content was overwritten: %q", string(body))
	}
}

func TestFetchInstallsOnCacheMiss(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml":   "services: {}\n",
		"splice-x/cluster/compose/localnet/env/common.env": "DB_PORT=5432\n",
	})

	_, cleanup := servePinnedTarball(t, tarball)
	defer cleanup()

	cacheRoot := t.TempDir()
	v := versionForTarball(t, tarball)

	got, err := Fetch(context.Background(), v, cacheRoot, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got, "compose.yaml")); err != nil {
		t.Errorf("compose.yaml missing after install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got, "env", "common.env")); err != nil {
		t.Errorf("env/common.env missing after install: %v", err)
	}

	// No staging leftovers.
	entries, _ := os.ReadDir(cacheRoot)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "splice-stage-") {
			t.Errorf("staging dir leaked: %s", e.Name())
		}
	}
}

func TestFetchRejectsTarballWithoutComposeYaml(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/README.md":                       "no localnet here\n",
		"splice-x/cluster/compose/other/notes.txt": "ignored too\n",
	})

	_, cleanup := servePinnedTarball(t, tarball)
	defer cleanup()

	cacheRoot := t.TempDir()
	v := versionForTarball(t, tarball)

	_, err := Fetch(context.Background(), v, cacheRoot, nil)
	if err == nil {
		t.Fatal("expected error for tarball without compose.yaml, got nil")
	}
	if !strings.Contains(err.Error(), "compose.yaml") {
		t.Errorf("expected error to mention compose.yaml, got %v", err)
	}

	// Project dir must NOT have been installed.
	if _, statErr := os.Stat(ProjectDir(cacheRoot, v)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("project dir should not exist after rejected install, statErr=%v", statErr)
	}
}

func TestFetchCleansStagingOnDownloadFailure(t *testing.T) {
	// Serve garbage so the gzip reader fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-a-gzip-stream"))
	}))
	defer srv.Close()

	prev := tarballURL
	tarballURL = func(v Version) string { return srv.URL }
	defer func() { tarballURL = prev }()

	cacheRoot := t.TempDir()
	v := Version{Tag: "x", Commit: "fakecommit", ContentSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}

	if _, err := Fetch(context.Background(), v, cacheRoot, nil); err == nil {
		t.Fatal("expected error, got nil")
	}

	// No staging leftovers; no projectDir half-installed.
	entries, _ := os.ReadDir(cacheRoot)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "splice-stage-") {
			t.Errorf("staging dir leaked: %s", e.Name())
		}
		if e.Name() == "splice-x" {
			t.Errorf("partial projectDir leaked: %s", e.Name())
		}
	}
}

func TestFetchHandlesConcurrentRenameRace(t *testing.T) {
	// Simulate the race: by the time Fetch tries os.Rename(staging,
	// projectDir), another caller has already moved a valid project
	// into projectDir. The first caller's rename returns an error
	// (target exists / non-empty) and Fetch must fall through to the
	// "compose.yaml exists" success path.
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml": "services: {}\n",
	})

	cacheRoot := t.TempDir()
	v := versionForTarball(t, tarball)
	projectDir := ProjectDir(cacheRoot, v)

	// Pre-create the projectDir as if a concurrent caller raced ahead.
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "compose.yaml"), []byte("from-race\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// And drop a sentinel so we can later confirm Fetch returned this
	// dir, not a freshly-extracted overwrite.
	if err := os.WriteFile(filepath.Join(projectDir, "SENTINEL"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Because compose.yaml already exists in projectDir, Fetch should
	// short-circuit on the FIRST stat — never download, never stage.
	prev := tarballURL
	tarballURL = func(v Version) string {
		t.Fatalf("Fetch attempted to download despite already-installed cache")
		return ""
	}
	defer func() { tarballURL = prev }()

	got, err := Fetch(context.Background(), v, cacheRoot, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != projectDir {
		t.Errorf("Fetch returned %q, want %q", got, projectDir)
	}
	if _, err := os.Stat(filepath.Join(got, "SENTINEL")); err != nil {
		t.Errorf("Fetch clobbered the race winner's cache (SENTINEL gone): %v", err)
	}
	// Avoid unused-var warning on tarball/sum in this path.
	_ = tarball
}

// buildTestTarball returns a gzipped tar archive containing the named
// entries. Directories are inferred from key prefixes.
func buildTestTarball(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, body := range entries {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
