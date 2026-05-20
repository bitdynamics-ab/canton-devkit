package splice

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// localnetSubdirSuffix is the path inside the tarball that contains the
	// compose project. GitHub source tarballs are prefixed with
	// `<repo>-<tag>/` which we strip during extraction.
	localnetSubdirSuffix = "cluster/compose/localnet"

	// downloadTimeout is the maximum time allowed for the HTTP transfer.
	downloadTimeout = 5 * time.Minute
)

// tarballURL is the function Fetch uses to compute the GitHub source-
// tarball URL for a given Splice version. It's a package var (not a
// const) so tests can point Fetch at an httptest.Server without
// monkey-patching the entire HTTP client.
var tarballURL = func(v Version) string {
	return fmt.Sprintf("https://github.com/canton-network/splice/archive/refs/tags/%s.tar.gz", v.Tag)
}

// Fetch ensures the compose project for v is present and verified under
// cacheRoot. Returns the absolute path of the project directory (the one
// containing `compose.yaml`).
//
// Cache semantics:
//   - If the project directory already exists and contains compose.yaml,
//     Fetch returns immediately. A previously-verified tarball does not
//     need to be re-downloaded or re-hashed.
//   - On cache miss, the tarball is downloaded, its SHA256 is verified
//     against v.SHA256, and only the cluster/compose/localnet subtree is
//     extracted. SHA256 mismatch is fatal — the partial cache is removed.
//
// Fetch never modifies anything outside cacheRoot and never writes to the
// system temp dir for the final cache (only short-lived staging dirs).
func Fetch(ctx context.Context, v Version, cacheRoot string, progress io.Writer) (string, error) {
	projectDir := ProjectDir(cacheRoot, v)
	if _, err := os.Stat(filepath.Join(projectDir, "compose.yaml")); err == nil {
		return projectDir, nil
	}

	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", fmt.Errorf("create cache root %s: %w", cacheRoot, err)
	}

	// Stage extraction under a sibling temp dir so a failure mid-way can
	// never leave a half-populated projectDir that future runs would
	// mistake for a complete cache.
	stagingDir, err := os.MkdirTemp(cacheRoot, "splice-stage-")
	if err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	if progress != nil {
		_, _ = fmt.Fprintf(progress, "Fetching Splice LocalNet %s (~%d MB)...\n",
			v.Tag, v.Size/(1024*1024))
	}

	url := tarballURL(v)
	// Cap the body at 4× the catalogued size. A misconfigured or malicious
	// server returning a multi-gigabyte stream would otherwise OOM the
	// host; 4× gives headroom for legitimate variance in GitHub source-
	// tarball gzip metadata without inviting abuse.
	maxBytes := v.Size * 4
	if maxBytes <= 0 {
		// Unknown size in the catalogue → fall back to a hard 512 MB cap.
		maxBytes = 512 * 1024 * 1024
	}
	if err := downloadAndExtract(ctx, url, v.SHA256, maxBytes, stagingDir); err != nil {
		// Staging dir is cleaned by the deferred RemoveAll above; nothing
		// extra needed here.
		return "", err
	}

	// Defensive: confirm the tarball actually contained the expected
	// localnet subtree before promoting staging → projectDir. Otherwise
	// we'd cache an empty directory and future runs would short-circuit
	// the (broken) cache-hit path.
	if _, err := os.Stat(filepath.Join(stagingDir, "compose.yaml")); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("tarball %s missing %s/compose.yaml",
				url, localnetSubdirSuffix)
		}
		return "", fmt.Errorf("stat staged compose.yaml: %w", err)
	}

	if err := os.Rename(stagingDir, projectDir); err != nil {
		// If another concurrent Fetch already moved a directory into
		// projectDir, treat that as a success — the artifact is now
		// present and verified.
		if _, statErr := os.Stat(filepath.Join(projectDir, "compose.yaml")); statErr == nil {
			return projectDir, nil
		}
		return "", fmt.Errorf("install cache: %w", err)
	}

	if progress != nil {
		_, _ = fmt.Fprintf(progress, "Cached at %s\n", projectDir)
	}
	return projectDir, nil
}

// downloadAndExtract streams the tarball at url, verifies its SHA256 in
// flight, and extracts the cluster/compose/localnet subtree into destDir.
// Uses a single streaming pass so a 130 MB tarball never lands on disk
// whole — only the ~few-hundred-KB compose subtree is persisted.
//
// The body is wrapped in io.LimitReader(maxBytes+1) so a server that
// returns an arbitrary-sized stream (misconfigured CDN, malicious
// proxy) can't OOM the host. The +1 lets us tell "exactly maxBytes"
// from "more than maxBytes" — only the latter is an attack signal.
func downloadAndExtract(ctx context.Context, url, wantSHA string, maxBytes int64, destDir string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	limited := &cappedReader{r: io.LimitReader(resp.Body, maxBytes+1), max: maxBytes}
	hasher := sha256.New()
	teed := io.TeeReader(limited, hasher)

	gzr, err := gzip.NewReader(teed)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	if err := extractLocalNet(tar.NewReader(gzr), destDir); err != nil {
		return err
	}

	// Drain remaining bytes so the SHA256 covers the full tarball, even
	// if the subdir extraction finished before EOF.
	if _, err := io.Copy(io.Discard, teed); err != nil {
		return fmt.Errorf("drain tarball: %w", err)
	}

	if limited.overflow {
		return fmt.Errorf("download %s: response exceeded %d-byte cap", url, maxBytes)
	}

	gotSHA := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotSHA, wantSHA) {
		return fmt.Errorf("SHA256 mismatch for %s: got %s, want %s", url, gotSHA, wantSHA)
	}
	return nil
}

// cappedReader wraps an io.LimitReader and remembers whether the cap
// was hit. A bare io.LimitReader silently truncates at the limit, which
// would look identical to a short legitimate body — we need to
// distinguish "stream too large" from "valid stream that happened to be
// shorter than the cap."
type cappedReader struct {
	r        io.Reader
	max      int64
	read     int64
	overflow bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	if c.read > c.max {
		c.overflow = true
	}
	return n, err
}

// extractLocalNet walks a tar reader and writes only entries inside the
// cluster/compose/localnet/ subdir (relative to the GitHub-archive top-
// level directory) into destDir, stripping that prefix.
//
// Defends against path traversal: any entry whose cleaned destination
// escapes destDir is rejected with an error.
func extractLocalNet(tr *tar.Reader, destDir string) error {
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		rel, ok := stripArchivePrefix(hdr.Name)
		if !ok {
			continue
		}

		target, err := safeJoin(destDir, rel)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}
		default:
			// Skip symlinks, char/block devices, fifos. The Splice
			// LocalNet tree is regular files and dirs only.
		}
	}
}

// stripArchivePrefix takes a tar entry name like
// `splice-0.6.4/cluster/compose/localnet/compose.yaml` and returns
// `compose.yaml`. Returns ok=false for entries outside that subtree.
func stripArchivePrefix(name string) (string, bool) {
	// GitHub source archives use forward slashes regardless of host OS.
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 {
		return "", false
	}
	rest := parts[1]
	const prefix = localnetSubdirSuffix + "/"
	if !strings.HasPrefix(rest, prefix) {
		return "", false
	}
	return rest[len(prefix):], true
}

// safeJoin joins base + rel and rejects results that escape base. This
// blocks classic tar traversal payloads like `../../etc/passwd`.
//
// Invariant: `filepath.Clean(base)` strips any trailing separator, so
// `cleanedBase` ends in exactly one separator. We then test that
// `full+sep` starts with `cleanedBase` — appending the separator to
// `full` prevents the false-positive "base/foo" matching the prefix
// "base/foobar/". Both ends are normalised before comparison so OS-
// specific separators (Windows backslash) work transparently.
func safeJoin(base, rel string) (string, error) {
	full := filepath.Join(base, rel)
	cleanedBase := filepath.Clean(base) + string(os.PathSeparator)
	if !strings.HasPrefix(full+string(os.PathSeparator), cleanedBase) {
		return "", fmt.Errorf("refusing path traversal in tar entry: %s", rel)
	}
	return full, nil
}
