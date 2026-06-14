package types

// Snapshot describes one capture produced by `localnet snapshot
// --name X --to file.tgz` and accepted by `restore --from`.
//
// The header is written to the archive's first entry (snapshot.json)
// so a restore can verify it's looking at a DevKit snapshot for the
// right Splice version before touching disk.
//
// A snapshot is a logical PostgreSQL dump (pg_dumpall) of the
// instance's single Postgres container — the same backup method Canton
// and Splice document for participant/synchronizer nodes — rather than
// a raw copy of the docker volumes. Everything a LocalNet keeps
// (ledger, contracts, parties, node identities/keys) lives in that one
// Postgres, so the dump is the whole instance.
type Snapshot struct {
	SchemaVersion int    `json:"schema_version"`
	Instance      string `json:"instance"`
	SpliceVersion string `json:"splice_version"`
	CreatedAt     string `json:"created_at"` // RFC3339
	DevKitVersion string `json:"devkit_version"`

	// Database is the logical-dump descriptor. Present on every
	// snapshot this DevKit writes; a nil value marks a pre-dump-format
	// (volume-era) archive that this binary refuses to restore.
	Database *SnapshotDatabase `json:"database,omitempty"`
}

// SnapshotDatabase describes the single pg_dumpall capture in the
// archive (archive entry `database/dumpall.sql`).
type SnapshotDatabase struct {
	// Engine is always "postgresql" today; recorded so a future
	// alternate store can be told apart without a schema bump.
	Engine string `json:"engine"`

	// PostgresImage is the image of the instance's Postgres container at
	// capture time (e.g. "postgres:14"). Restore spins up a throwaway
	// container from this image to load the dump into the instance's
	// data volume, so the binary matches the on-disk PGDATA major
	// version even when the dump is restored into a fresh volume.
	PostgresImage string `json:"postgres_image"`

	// User is the Postgres superuser the dump was taken as and must be
	// loaded as (Splice LocalNet uses a single "cnadmin").
	User string `json:"user"`

	// VolumeSuffix is the docker-compose volume key holding PGDATA
	// (e.g. "postgres"); the restore target volume is
	// "<compose-project>_<VolumeSuffix>".
	VolumeSuffix string `json:"volume_suffix"`

	// DatabaseCount is the number of application databases captured —
	// informational, surfaced in the success banner.
	DatabaseCount int `json:"database_count"`

	// SizeBytes is the uncompressed dump size; used for the restore
	// disk preflight.
	SizeBytes int64 `json:"size_bytes"`

	// ContentSHA is the sha256 of the uncompressed dump stream, verified
	// on restore (format "sha256:<hex>").
	ContentSHA string `json:"content_sha"`
}
