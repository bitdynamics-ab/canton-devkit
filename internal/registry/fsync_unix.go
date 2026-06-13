//go:build unix

package registry

import "os"

// fsyncDir flushes the directory entry for dir to stable storage so a
// rename into dir survives a power loss. On Unix this is an fsync of an
// O_RDONLY handle on the directory — the canonical way to make a
// just-renamed file's directory entry durable.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	// Sync first, then surface the close error if Sync succeeded.
	if serr := d.Sync(); serr != nil {
		_ = d.Close()
		return serr
	}
	return d.Close()
}
