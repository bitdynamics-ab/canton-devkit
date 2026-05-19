package splice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if err := downloadAndExtract(context.Background(), srv.URL, wantSHA, dest); err != nil {
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
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
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
		t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("expected SHA256 mismatch, got %v", err)
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
