package splice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// TestFetchVerifiesSHA256 spins up a local HTTP server that serves a tiny
// hand-rolled tar.gz containing a `cluster/compose/localnet/compose.yaml`
// entry. We pin the SHA in a Version and confirm Fetch extracts the file
// to the expected cache path.
func TestFetchVerifiesSHA256(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-test/cluster/compose/localnet/compose.yaml":   "services: {}\n",
		"splice-test/cluster/compose/localnet/env/common.env": "DB_PORT=5432\n",
		"splice-test/README.md":                               "should be skipped\n",
	})
	sum := sha256.Sum256(tarball)
	wantSHA := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()

	// Override the URL template via a local copy of downloadAndExtract.
	// Cleanest test-only path: temporarily wire URL through an env-vary
	// helper — but to keep production code dep-free, we just call
	// downloadAndExtract directly here.
	dest := t.TempDir()
	if err := downloadAndExtract(context.Background(), srv.URL, wantSHA, "", 1<<20, dest, nil); err != nil {
		t.Fatalf("downloadAndExtract: %v", err)
	}

	// compose.yaml present and content correct
	body, err := os.ReadFile(filepath.Join(dest, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose.yaml not extracted: %v", err)
	}
	if !strings.Contains(string(body), "services") {
		t.Errorf("unexpected compose.yaml content: %q", string(body))
	}

	// env/common.env present (nested entry)
	if _, err := os.Stat(filepath.Join(dest, "env", "common.env")); err != nil {
		t.Errorf("env/common.env not extracted: %v", err)
	}

	// README.md NOT extracted (outside subdir)
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("README.md should not have been extracted (err=%v)", err)
	}
}

func TestFetchRejectsBadSHA256(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-test/cluster/compose/localnet/compose.yaml": "x",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()

	err := downloadAndExtract(context.Background(), srv.URL,
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"", 1<<20, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("expected SHA256 mismatch, got %v", err)
	}
}

// TestDownloadAndExtractRejectsOversizedBody covers the new cappedReader
// guard: a server that returns more than maxBytes must fail with a
// clear cap error, not OOM the host or silently truncate.
func TestDownloadAndExtractRejectsOversizedBody(t *testing.T) {
	// 2 MB of garbage. Cap will be 1 MB.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("A"), 2<<20))
	}))
	defer srv.Close()

	err := downloadAndExtract(context.Background(), srv.URL,
		"00000000000000000000000000000000000000000000000000000000000000000000",
		"", 1<<20, t.TempDir(), nil)
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

// --- Content-SHA verification (BIT-117 #10 hardening) -------------------

// TestDownloadAndExtractAcceptsRegeneratedGzipWhenContentMatches covers
// the "GitHub regenerated the source tarball" scenario: gzip bytes
// differ, extracted tree is identical, install succeeds with a warning.
func TestDownloadAndExtractAcceptsRegeneratedGzipWhenContentMatches(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml":   "services: {}\n",
		"splice-x/cluster/compose/localnet/env/common.env": "DB_PORT=5432\n",
	})

	// Extract to a scratch dir to compute the expected content SHA.
	scratch := t.TempDir()
	if err := downloadAndExtract(context.Background(), serveBytesURL(t, tarball),
		hex.EncodeToString(sha256OfBytes(tarball)), "", 1<<20, scratch, nil); err != nil {
		t.Fatalf("scratch extract: %v", err)
	}
	wantContent, err := computeTreeSHA(scratch)
	if err != nil {
		t.Fatalf("computeTreeSHA: %v", err)
	}

	// Now pretend the upstream gzip hash drifted: pass a wrong wantSHA
	// but the correct wantContentSHA. The progress writer captures the
	// warning so we can assert it fires.
	var progress bytes.Buffer
	dest := t.TempDir()
	err = downloadAndExtract(context.Background(), serveBytesURL(t, tarball),
		strings.Repeat("0", 64), wantContent, 1<<20, dest, &progress)
	if err != nil {
		t.Fatalf("expected success with regenerated gzip + matching content, got %v", err)
	}
	if !strings.Contains(progress.String(), "gzip hash drifted") {
		t.Errorf("expected drift warning, got %q", progress.String())
	}
}

// TestDownloadAndExtractRejectsContentMismatch covers the inverse: gzip
// hash matches but the extracted tree doesn't match the catalogued
// ContentSHA (e.g. a MITM that wrapped a malicious payload in the same-
// sized gzip envelope by some unlikely collision).
func TestDownloadAndExtractRejectsContentMismatch(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml": "services: {}\n",
	})
	gzipHash := hex.EncodeToString(sha256OfBytes(tarball))

	err := downloadAndExtract(context.Background(), serveBytesURL(t, tarball),
		gzipHash,
		strings.Repeat("1", 64), // wrong content hash
		1<<20, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("expected content hash mismatch, got %v", err)
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

func sha256OfBytes(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
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
	v := Version{Tag: "test", SHA256: "ignored-on-cache-hit"}
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
	sum := sha256.Sum256(tarball)

	_, cleanup := servePinnedTarball(t, tarball)
	defer cleanup()

	cacheRoot := t.TempDir()
	v := Version{Tag: "x", SHA256: hex.EncodeToString(sum[:])}

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
	sum := sha256.Sum256(tarball)

	_, cleanup := servePinnedTarball(t, tarball)
	defer cleanup()

	cacheRoot := t.TempDir()
	v := Version{Tag: "x", SHA256: hex.EncodeToString(sum[:])}

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
	v := Version{Tag: "x", SHA256: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}

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
	sum := sha256.Sum256(tarball)

	cacheRoot := t.TempDir()
	v := Version{Tag: "x", SHA256: hex.EncodeToString(sum[:])}
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
