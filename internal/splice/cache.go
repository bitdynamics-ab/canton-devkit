package splice

import (
	"os"
	"path/filepath"
)

// CacheRoot returns the directory under which extracted Splice LocalNet
// compose projects are cached. One subdirectory per tag+commit:
//
//	~/.canton-devkit/cache/splice-0.6.4-578b7822/
//	  ├── compose.yaml
//	  ├── compose.env
//	  ├── env/
//	  ├── conf/
//	  └── docker/
//
// The cache is shared across LocalNet instances — only the per-instance
// overlay env is written to ~/.canton-devkit/localnet/<name>/.
//
// We use ~/.canton-devkit (not ~/.canton): the latter is reserved by
// upstream Canton tooling. Matches internal/registry.Root().
func CacheRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to relative path so we still work in CI/containers
		// without a real $HOME.
		return filepath.Join(".", ".canton-devkit", "cache")
	}
	return filepath.Join(home, ".canton-devkit", "cache")
}

// ProjectDir is the cache subdirectory holding the extracted compose
// project for a specific Splice version.
//
// The directory name includes BOTH the tag and a short commit prefix
// (e.g. splice-0.6.4-578b7822). Keying on the tag alone was unsafe for
// mutable-stream catalogue entries: token-standard-v2 tracks an upstream
// branch that "can be reset on a weekly cadence", so re-pinning it to a
// new commit must not silently reuse the old extracted tree while the
// image tags advance. Folding the commit into the key makes a re-pin
// land in a fresh directory and forces a re-fetch + re-verify. The
// commit is empty only in degenerate test fixtures; when empty we fall
// back to the tag-only name so those paths keep working.
func ProjectDir(cacheRoot string, v Version) string {
	name := "splice-" + v.Tag
	if c := shortCommit(v.Commit); c != "" {
		name += "-" + c
	}
	return filepath.Join(cacheRoot, name)
}

// shortCommit returns the first 8 chars of a git SHA (or the whole
// string if shorter). Empty in → empty out.
func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
