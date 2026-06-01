package telemetry

import (
	"io/fs"
	"path/filepath"
	"strings"
)

func walk(root string, fn func(path string)) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (strings.Contains(p, "/node_modules") || strings.Contains(p, "/.git") || strings.HasSuffix(p, "/dist")) {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			fn(p)
		}
		return nil
	})
}
