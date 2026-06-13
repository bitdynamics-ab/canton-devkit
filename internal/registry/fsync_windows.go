//go:build windows

package registry

// fsyncDir is a no-op on Windows. Windows has no portable way to fsync a
// directory handle (CreateFile on a directory requires
// FILE_FLAG_BACKUP_SEMANTICS and FlushFileBuffers on it is not
// guaranteed to flush directory metadata), and NTFS commits the rename's
// metadata through its own log. The temp file's contents are still
// flushed via (*os.File).Sync before the rename in atomicWrite, which is
// the part that matters most for avoiding truncated state files.
func fsyncDir(string) error { return nil }
