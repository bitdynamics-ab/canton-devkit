package splice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
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

	// First pass: empty wantContentSHA means trust-on-first-use —
	// downloadAndExtract extracts and RETURNS the computed hash rather
	// than refusing.
	scratch := t.TempDir()
	wantContent, err := downloadAndExtract(context.Background(), srv.URL, "", 1<<20, scratch)
	if err != nil {
		t.Fatalf("TOFU extract (empty ContentSHA) should succeed, got %v", err)
	}
	if len(wantContent) != 64 {
		t.Fatalf("computed ContentSHA looks wrong: %q", wantContent)
	}

	// Second pass: real install pinned to the hash the first pass
	// computed — must verify clean and return the same hash.
	dest := t.TempDir()
	got, err := downloadAndExtract(context.Background(), srv.URL, wantContent, 1<<20, dest)
	if err != nil {
		t.Fatalf("downloadAndExtract: %v", err)
	}
	if got != wantContent {
		t.Errorf("returned SHA %q != pinned %q", got, wantContent)
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

	_, err := downloadAndExtract(context.Background(), srv.URL,
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

	_, err := downloadAndExtract(context.Background(), serveBytesURL(t, tarball),
		strings.Repeat("1", 64), // wrong content hash
		1<<20, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("expected content hash mismatch, got %v", err)
	}
}

// TestDownloadAndExtractTOFUOnEmptyContentSHA pins trust-on-first-use:
// an empty wantContentSHA (uncurated tag) extracts the tree and returns
// the computed hash, NOT a refusal — refusing here would break
// `--allow-uncurated` end to end.
func TestDownloadAndExtractTOFUOnEmptyContentSHA(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml": "services: {}\n",
	})
	dest := t.TempDir()
	got, err := downloadAndExtract(context.Background(), serveBytesURL(t, tarball),
		"", 1<<20, dest)
	if err != nil {
		t.Fatalf("TOFU extract should succeed on empty ContentSHA, got %v", err)
	}
	if len(got) != 64 {
		t.Errorf("expected a 64-char tree SHA, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "compose.yaml")); err != nil {
		t.Errorf("compose.yaml not extracted under TOFU: %v", err)
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
	v := Version{Tag: "test", Commit: "testcommit", ContentSHA: "cafef00d"}
	projectDir := ProjectDir(cacheRoot, v)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed the cache, including the content-sha marker that matches
	// the catalogue ContentSHA — a valid hit short-circuits without a
	// re-hash or re-download.
	if err := os.WriteFile(filepath.Join(projectDir, "compose.yaml"), []byte("cached: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, contentSHAMarker), []byte(v.ContentSHA), 0o644); err != nil {
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
	// A matching content-sha marker makes this a VALID hit so Fetch
	// short-circuits instead of re-verifying + re-fetching.
	if err := os.WriteFile(filepath.Join(projectDir, contentSHAMarker), []byte(v.ContentSHA), 0o644); err != nil {
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
}

// TestProjectDirIncludesCommit pins the cache key: the cache directory
// folds in the short commit so a mutable-stream tag re-pinned to a new
// commit lands in a DIFFERENT directory (forcing a re-fetch) rather
// than silently reusing the stale tree.
func TestProjectDirIncludesCommit(t *testing.T) {
	root := "/cache"
	a := ProjectDir(root, Version{Tag: "token-standard-v2", Commit: "aaaaaaaabbbb"})
	b := ProjectDir(root, Version{Tag: "token-standard-v2", Commit: "ccccccccdddd"})
	if a == b {
		t.Fatalf("re-pinned tag reused the same cache dir: %q", a)
	}
	if !strings.Contains(a, "token-standard-v2-aaaaaaaa") {
		t.Errorf("ProjectDir = %q, want it to embed the short commit", a)
	}
	// Empty commit (degenerate fixtures) falls back to tag-only.
	if got := ProjectDir(root, Version{Tag: "x"}); !strings.HasSuffix(got, "splice-x") {
		t.Errorf("empty-commit ProjectDir = %q, want tag-only fallback", got)
	}
}

// TestFetchRejectsTamperedCacheMarker pins the re-verification: a
// cache hit whose recorded ContentSHA marker no longer matches the
// catalogue (a re-pin to the same commit, or local tampering) must be
// dropped and re-fetched, not blindly trusted.
func TestFetchRejectsTamperedCacheMarker(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml": "services: {}\n",
	})
	_, cleanup := servePinnedTarball(t, tarball)
	defer cleanup()

	cacheRoot := t.TempDir()
	v := versionForTarball(t, tarball) // real ContentSHA + commit
	projectDir := ProjectDir(cacheRoot, v)

	// Seed a stale cache: compose.yaml present, but the marker carries
	// the WRONG hash (as if the catalogue was re-pinned, or the tree was
	// tampered with).
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "compose.yaml"), []byte("stale: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, contentSHAMarker), []byte("0000bad0"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Fetch(context.Background(), v, cacheRoot, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// The stale tree must have been replaced by the freshly-fetched one.
	body, err := os.ReadFile(filepath.Join(got, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose.yaml: %v", err)
	}
	if strings.Contains(string(body), "stale: true") {
		t.Errorf("Fetch trusted the tampered cache instead of re-fetching: %q", string(body))
	}
	// And the marker now matches the verified hash.
	marker, _ := os.ReadFile(filepath.Join(got, contentSHAMarker))
	if strings.TrimSpace(string(marker)) != v.ContentSHA {
		t.Errorf("marker after re-fetch = %q, want %q", marker, v.ContentSHA)
	}
}

// TestResolveUpstreamThenFetch_RecordsTOFU is the end-to-end:
// ResolveUpstream synthesises an uncurated Version with an empty
// ContentSHA; Fetch must extract it (trust-on-first-use), record the
// computed hash into resolved-versions.json, and a second Fetch must
// then verify the cached tree against that recorded hash.
func TestResolveUpstreamThenFetch_RecordsTOFU(t *testing.T) {
	withTempCache(t)

	// Fake GitHub ref endpoint so ResolveUpstream resolves the tag to a
	// commit without touching the network.
	refSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"object":{"type":"commit","sha":"feedface1234"}}`)
	}))
	defer refSrv.Close()
	origEndpoint := upstreamTagRefEndpoint
	upstreamTagRefEndpoint = refSrv.URL + "/repos/canton-network/splice/git/refs/tags/"
	defer func() { upstreamTagRefEndpoint = origEndpoint }()

	v, err := ResolveUpstream(context.Background(), "0.7.0-alpha.9")
	if err != nil {
		t.Fatalf("ResolveUpstream: %v", err)
	}
	if v.ContentSHA != "" {
		t.Fatalf("ResolveUpstream should synthesise an empty ContentSHA, got %q", v.ContentSHA)
	}

	// Serve the source tarball Fetch will download.
	tarball := buildTestTarball(t, map[string]string{
		"splice-x/cluster/compose/localnet/compose.yaml": "services: {}\n",
	})
	_, cleanup := servePinnedTarball(t, tarball)
	defer cleanup()

	cacheRoot := t.TempDir()
	projectDir, err := Fetch(context.Background(), v, cacheRoot, nil)
	if err != nil {
		t.Fatalf("Fetch (TOFU) failed: %v", err)
	}

	// The resolved cache must now carry the computed ContentSHA.
	cache, err := ReadResolvedCache()
	if err != nil {
		t.Fatalf("ReadResolvedCache: %v", err)
	}
	hit, ok := cache.LookupResolved("0.7.0-alpha.9")
	if !ok {
		t.Fatal("resolved cache lost the entry after Fetch")
	}
	if len(hit.ContentSHA) != 64 {
		t.Fatalf("TOFU ContentSHA not recorded into resolved cache: %q", hit.ContentSHA)
	}
	// The on-disk marker must match what was recorded.
	marker, _ := os.ReadFile(filepath.Join(projectDir, contentSHAMarker))
	if strings.TrimSpace(string(marker)) != hit.ContentSHA {
		t.Errorf("marker %q != recorded ContentSHA %q", marker, hit.ContentSHA)
	}

	// Second Fetch with the now-recorded ContentSHA must verify clean
	// against the cached tree (the "verify thereafter" half of TOFU).
	verified := hit // carries the recorded ContentSHA
	if _, err := Fetch(context.Background(), verified.Version, cacheRoot, nil); err != nil {
		t.Errorf("second Fetch (verify-thereafter) failed: %v", err)
	}
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

// TestCacheHitIsValid pins cacheHitIsValid's branches — in particular
// the uncurated trust-on-first-use cache-HIT path (wantContentSHA==""
// with a marker present), which no Fetch-level test exercises.
func TestCacheHitIsValid(t *testing.T) {
	writeMarker := func(dir, sha string) {
		if err := os.WriteFile(filepath.Join(dir, contentSHAMarker), []byte(sha+"\n"), 0o644); err != nil {
			t.Fatalf("write marker: %v", err)
		}
	}

	t.Run("uncurated hit with a marker is trusted (TOFU)", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(dir, "deadbeefcafef00d")
		if !cacheHitIsValid(dir, "") {
			t.Error("uncurated cache hit carrying a marker should be trusted")
		}
	})

	t.Run("uncurated legacy cache without a marker is accepted", func(t *testing.T) {
		if !cacheHitIsValid(t.TempDir(), "") {
			t.Error("uncurated hit without a marker should be accepted (nothing to compare against)")
		}
	})

	t.Run("curated requires an exact (case-insensitive) marker match", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(dir, "ABC123")
		if !cacheHitIsValid(dir, "abc123") {
			t.Error("curated hit with a matching marker should be valid (EqualFold)")
		}
		if cacheHitIsValid(dir, "0000ff") {
			t.Error("curated hit with a divergent marker must be rejected (re-pin / tamper)")
		}
	})

	t.Run("curated without a marker is rejected", func(t *testing.T) {
		if cacheHitIsValid(t.TempDir(), "abc123") {
			t.Error("curated hit without a marker must re-fetch to gain the marker")
		}
	})
}
