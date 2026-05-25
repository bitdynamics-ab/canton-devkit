package types

// Snapshot describes one capture produced by `localnet snapshot
// --name X --to file.tgz` (P1-10) and accepted by `restore --from`.
//
// The header is written to the archive's first entry (snapshot.json)
// so a restore can verify it's looking at a DevKit snapshot for the
// right Splice version before touching disk.
type Snapshot struct {
	SchemaVersion int    `json:"schema_version"`
	Instance      string `json:"instance"`
	SpliceVersion string `json:"splice_version"`
	CreatedAt     string `json:"created_at"` // RFC3339
	DevKitVersion string `json:"devkit_version"`

	// Volumes captured. Each entry corresponds to one docker volume
	// extracted via `docker run --rm -v <vol>:/src alpine tar`.
	Volumes []SnapshotVolume `json:"volumes"`
}

// SnapshotVolume is one entry in Snapshot.Volumes.
type SnapshotVolume struct {
	Name       string `json:"name"` // docker volume name
	SizeBytes  int64  `json:"size_bytes"`
	ContentSHA string `json:"content_sha"` // sha256 of the tar stream
}
