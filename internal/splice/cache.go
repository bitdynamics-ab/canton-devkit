package splice

import (
	"os"
	"path/filepath"
)

// CacheRoot returns the directory under which extracted Splice LocalNet
// compose projects are cached. One subdirectory per tag:
//
//	~/.canton-devkit/cache/splice-0.6.4/
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
// project for a specific Splice tag.
func ProjectDir(cacheRoot string, v Version) string {
	return filepath.Join(cacheRoot, "splice-"+v.Tag)
}
