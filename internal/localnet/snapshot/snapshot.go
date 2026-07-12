package snapshot

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

// Implements `dpm localnet snapshot` + `restore`.
//
// A LocalNet keeps all of its state (ledger, contracts, parties, node
// identities/keys) in a single PostgreSQL container. So a snapshot is a
// logical `pg_dumpall` of that Postgres, and a restore loads it back,
// the backup/restore method Canton and Splice document for
// participant/synchronizer nodes. This decouples snapshot/restore from
// the docker-compose volume layout (no volume-name discovery, no
// compose-label re-stamping) and makes archives portable across
// Postgres point releases.
//
// Key safety properties:
//
//  1. Zip Slip on restore is rejected: only the strict entry path
//     `database/dumpall.sql` is honoured.
//  2. Nothing is buffered whole in RAM: pg_dumpall streams to a temp
//     file then into the archive on capture, and the archive streams
//     to psql on restore.
//  3. registry.State is captured as the SECOND archive entry so a
//     restored snapshot is bringable; restore re-registers via
//     registry.Write.
//  4. Content is verified: streaming sha256 on both writer + reader;
//     mismatch aborts.
//  5. Splice-version mismatch is refused (unless --force).
//
// On-disk layout (strict order):
//
//	snapshot.tgz
//	├── snapshot.json        (types.Snapshot header, FIRST)
//	├── state.json           (registry.State, SECOND)
//	└── database/dumpall.sql (pg_dumpall --clean --if-exists, THIRD)

const (
	// snapshotSchemaVersion is 2: v1 was the volume-tar format. A v1
	// archive (Database == nil) is refused rather than mis-restored.
	snapshotSchemaVersion = 2

	archiveHeaderName = "snapshot.json"
	archiveStateName  = "state.json"
	archiveDumpName   = "database/dumpall.sql"

	// pgDataDir is where the Postgres image stores PGDATA; the volume
	// mounted here is what restore loads into.
	pgDataDir = "/var/lib/postgresql/data"

	// maxDump caps the dump entry so a hostile header can't fill the
	// staging disk. 64 GiB is far above any realistic LocalNet logical
	// dump (a fresh instance is ~100 MB; a heavily-used one ~1 GB).
	maxDump = 64 << 30
)

// pgInfo is what ResolvePostgres discovers about a running instance's
// Postgres, recorded into the snapshot header so restore can rebuild
// the throwaway loader without the instance being up.
type pgInfo struct {
	Container    string
	User         string
	Image        string
	VolumeSuffix string // compose volume key: docker volume name minus the "<project>_" prefix
}

// pgArchiver is the seam tests fake; production uses dockerPgArchiver.
// All methods stream so neither side holds the dump in memory.
type pgArchiver interface {
	// ResolvePostgres finds the running Postgres container for the
	// compose project and reports the user/image/volume restore needs.
	ResolvePostgres(ctx context.Context, composeProject string) (pgInfo, error)
	// AnyContainerRunning reports whether any container of the project
	// is up — a guard so restore never starts a second writer against a
	// volume an instance is still using.
	AnyContainerRunning(ctx context.Context, composeProject string) (bool, error)
	// Quiesce pauses every container of the project EXCEPT pgContainer so
	// no node writes to Postgres while the dump runs — Canton's rule for a
	// single-step whole-cluster backup ("make sure no component writes to
	// the database while the backup is in progress"). It returns an
	// idempotent resume func that unpauses them; callers defer it so the
	// instance is never left paused on an error path.
	Quiesce(ctx context.Context, composeProject, pgContainer string) (resume func(), err error)
	// DumpAll streams `pg_dumpall --clean --if-exists` to w.
	DumpAll(ctx context.Context, container, user string, w io.Writer) error
	// CountDatabases reports the number of application databases (for
	// the success banner); best-effort, 0 on error.
	CountDatabases(ctx context.Context, container, user string) int
	// RestoreInto loads r (a pg_dumpall stream) into the named volume by
	// running a throwaway Postgres container from image, as user. It
	// returns an error only on a genuine failure — the two deterministic
	// role lines (the connection role can't drop or re-create itself)
	// are ignored.
	RestoreInto(ctx context.Context, image, volume, user string, r io.Reader) error
	// VolumeStorageAvailableBytes reports free space on Docker's volume
	// store (where the restored database lands), which on macOS/Windows
	// is inside the Docker VM and invisible to a host statfs.
	VolumeStorageAvailableBytes(ctx context.Context) (uint64, error)
}

var archiverFn pgArchiver = dockerPgArchiver{}

// SetArchiverForTest swaps the docker-backed archiver for a fake and
// returns a restore func. Used by callers in other packages (the Web UI
// snapshot handler tests) to exercise the happy path without docker.
func SetArchiverForTest(a pgArchiver) (restore func()) {
	prev := archiverFn
	archiverFn = a
	return func() { archiverFn = prev }
}

// FakeArchiver is an in-memory pgArchiver for tests — including tests in
// other packages (the Web UI snapshot handler), which can't name the
// unexported pgArchiver/pgInfo. Configure the dump it emits and inspect
// what a restore received. Production code never uses it.
type FakeArchiver struct {
	Image, User    string
	Dump           []byte // DumpAll writes this (defaults to a stub dump)
	DumpErr        error  // if set, DumpAll returns it
	DBCount        int
	Running        bool   // AnyContainerRunning result
	RestoreErr     error  // if set, RestoreInto returns it
	Restored       []byte // captures the bytes RestoreInto received
	RestoredVolume string // captures the restore target volume
	Quiesced       bool   // set true when Quiesce was called
	Resumed        bool   // set true when the resume func ran
}

func (f *FakeArchiver) Quiesce(context.Context, string, string) (func(), error) {
	f.Quiesced = true
	return func() { f.Resumed = true }, nil
}

func (f *FakeArchiver) ResolvePostgres(_ context.Context, project string) (pgInfo, error) {
	user := f.User
	if user == "" {
		user = "cnadmin"
	}
	image := f.Image
	if image == "" {
		image = "postgres:14"
	}
	return pgInfo{Container: project + "-postgres", User: user, Image: image,
		VolumeSuffix: "postgres"}, nil
}

func (f *FakeArchiver) AnyContainerRunning(context.Context, string) (bool, error) {
	return f.Running, nil
}

func (f *FakeArchiver) DumpAll(_ context.Context, _, _ string, w io.Writer) error {
	if f.DumpErr != nil {
		return f.DumpErr
	}
	body := f.Dump
	if body == nil {
		body = []byte("-- pg_dumpall\n")
	}
	_, err := w.Write(body)
	return err
}

func (f *FakeArchiver) CountDatabases(context.Context, string, string) int { return f.DBCount }

func (f *FakeArchiver) RestoreInto(_ context.Context, _, volume, _ string, r io.Reader) error {
	if f.RestoreErr != nil {
		return f.RestoreErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.Restored = b
	f.RestoredVolume = volume
	return nil
}

func (f *FakeArchiver) VolumeStorageAvailableBytes(context.Context) (uint64, error) {
	return 1 << 50, nil
}

// RunSnapshot streams a snapshot to dest. The instance must be running
// (pg_dumpall reads from a live Postgres). Two-pass: dump to an
// intermediate temp file (so the header can carry size + SHA), then
// write header → state.json → dump into the real archive in strict
// order.
func RunSnapshot(ctx context.Context, out io.Writer, errw io.Writer, name, dest string) int {
	// Per-instance exclusive lock — the SAME one `up`/`down` hold, so a
	// snapshot taken while the instance is being torn down refuses.
	release, lockErr := registry.Lock(name)
	if lockErr != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", lockErr)
		return localnet.ExitUserError
	}
	defer release()

	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			_, _ = fmt.Fprintf(errw, "no LocalNet instance named %q\n", name)
			return localnet.ExitUserError
		}
		_, _ = fmt.Fprintf(errw, "read registry: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	// pg_dumpall reads from a live Postgres, so the instance must be up.
	if state.Status != registry.StatusRunning {
		_, _ = fmt.Fprintf(errw,
			"instance %q is not running — a database snapshot reads from a live Postgres. "+
				"Run `localnet up %s` first.\n", name, name)
		return localnet.ExitUserError
	}

	pg, err := archiverFn.ResolvePostgres(ctx, state.ComposeProject)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "locate Postgres for %q: %s\n", state.ComposeProject, err)
		return localnet.ExitRuntimeFailure
	}

	_, _ = fmt.Fprintln(out, term.Prompt("", "", "", fmt.Sprintf(
		"dpm localnet snapshot %s %s %s",
		name, term.Amberc("--to"), dest)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Reading registry state", state.ComposeProject, ""))

	// Quiesce writers before the dump. pg_dumpall is a single-step
	// whole-cluster backup, and Canton's rule for that path is that no
	// component writes to the database while the backup is in progress.
	// With the node containers paused, every database is frozen at one
	// instant, so the cross-database ordering invariant holds (the
	// participant is never more recent than the sequencer, which would be
	// a ledger fork) and the capture is application-consistent.
	resume, qErr := archiverFn.Quiesce(ctx, state.ComposeProject, pg.Container)
	if qErr != nil {
		_, _ = fmt.Fprintf(errw, "quiesce writers: %s\n", qErr)
		return localnet.ExitRuntimeFailure
	}
	defer resume() // idempotent safety net; also called right after the dump
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Paused writers", "consistent capture", ""))
	_, _ = fmt.Fprintln(out, term.Step(term.StepBusy, "Dumping Postgres", pg.Container, ""))

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "create %s: %s\n", tmp, err)
		return localnet.ExitRuntimeFailure
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()

	// Pass 1: stream pg_dumpall to an intermediate temp file, hashing
	// and size-capping as we go.
	dumpPath, written, sum, err := streamDumpToTemp(ctx, pg, filepath.Dir(tmp))
	if err != nil {
		_ = f.Close()
		_, _ = fmt.Fprintf(errw, "dump database: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	resume() // dump captured: resume writers now (the deferred resume becomes a no-op)
	defer func() { _ = os.Remove(dumpPath) }()

	header := types.Snapshot{
		SchemaVersion: snapshotSchemaVersion,
		Instance:      name,
		SpliceVersion: state.SpliceVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		DevKitVersion: "dev",
		Database: &types.SnapshotDatabase{
			Engine:        "postgresql",
			PostgresImage: pg.Image,
			User:          pg.User,
			VolumeSuffix:  pg.VolumeSuffix,
			DatabaseCount: archiverFn.CountDatabases(ctx, pg.Container, pg.User),
			SizeBytes:     written,
			ContentSHA:    sum,
		},
	}

	// Pass 2: header → state.json → dump, in strict order.
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	headerBytes, _ := json.MarshalIndent(header, "", "  ")
	if err := writeTarEntry(tw, archiveHeaderName, headerBytes); err != nil {
		_, _ = fmt.Fprintf(errw, "write header: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	stateBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(errw, "marshal state: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	if err := writeTarEntry(tw, archiveStateName, stateBytes); err != nil {
		_, _ = fmt.Fprintf(errw, "write state.json: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	if err := streamTarEntry(tw, archiveDumpName, dumpPath, written); err != nil {
		_, _ = fmt.Fprintf(errw, "write dump: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	if err := tw.Close(); err != nil {
		_, _ = fmt.Fprintf(errw, "close tar: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	if err := gz.Close(); err != nil {
		_, _ = fmt.Fprintf(errw, "close gzip: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	if err := f.Close(); err != nil {
		_, _ = fmt.Fprintf(errw, "close file: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	if err := os.Rename(tmp, dest); err != nil {
		_, _ = fmt.Fprintf(errw, "rename %s → %s: %s\n", tmp, dest, err)
		return localnet.ExitRuntimeFailure
	}
	renamed = true

	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck,
		fmt.Sprintf("Captured %d database(s)", header.Database.DatabaseCount),
		fmt.Sprintf("%d MiB dump", written/1024/1024), ""))
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, term.Box(term.BoxBrand,
		fmt.Sprintf("%s Snapshot of %s saved to %s\n%s",
			term.Brandc("✦"),
			term.Bold("\""+name+"\""),
			term.Bold(dest),
			term.Dimc(fmt.Sprintf("%s · run %s to restore.",
				header.CreatedAt,
				term.Textc(fmt.Sprintf("localnet restore %s --from %s", name, dest)))))))
	return localnet.ExitSuccess
}

// streamDumpToTemp runs DumpAll into a temp file under dir, computing
// sha256 and a size ceiling as it goes.
func streamDumpToTemp(ctx context.Context, pg pgInfo, dir string) (string, int64, string, error) {
	dumpFile, err := os.CreateTemp(dir, "snap-dump-*.sql")
	if err != nil {
		return "", 0, "", fmt.Errorf("create dump tmp: %w", err)
	}
	dumpPath := dumpFile.Name()
	hasher := sha256.New()
	capW := newCappedWriter(dumpFile, maxDump)
	mw := io.MultiWriter(capW, hasher)
	if err := archiverFn.DumpAll(ctx, pg.Container, pg.User, mw); err != nil {
		_ = dumpFile.Close()
		_ = os.Remove(dumpPath)
		return "", 0, "", err
	}
	if capW.exceeded {
		_ = dumpFile.Close()
		_ = os.Remove(dumpPath)
		return "", 0, "", fmt.Errorf("dump exceeds %d-byte ceiling", maxDump)
	}
	if err := dumpFile.Close(); err != nil {
		_ = os.Remove(dumpPath)
		return "", 0, "", fmt.Errorf("close dump tmp: %w", err)
	}
	if capW.written == 0 {
		_ = os.Remove(dumpPath)
		return "", 0, "", fmt.Errorf("pg_dumpall produced an empty dump")
	}
	return dumpPath, capW.written, "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// RunRestore loads a snapshot's database dump back into the instance's
// Postgres volume. The instance must NOT be running (a restore drops
// and recreates every database; a live node would block it). The
// restore runs a throwaway Postgres against the data volume — no nodes,
// so no connections — then leaves the instance stopped for `up`.
func RunRestore(ctx context.Context, out io.Writer, errw io.Writer, name, src string, force bool) int {
	release, lockErr := registry.Lock(name)
	if lockErr != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", lockErr)
		return localnet.ExitUserError
	}
	defer release()

	f, err := os.Open(src)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "open %s: %s\n", src, err)
		return localnet.ExitUserError
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "gzip %s: %s\n", src, err)
		return localnet.ExitUserError
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	meta, err := readSnapshotHeader(tr)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return localnet.ExitUserError
	}
	if meta.SchemaVersion > snapshotSchemaVersion {
		_, _ = fmt.Fprintf(errw,
			"snapshot schema_version %d is newer than this DevKit's %d — upgrade\n",
			meta.SchemaVersion, snapshotSchemaVersion)
		return localnet.ExitUserError
	}
	if meta.Database == nil {
		_, _ = fmt.Fprintf(errw,
			"unsupported snapshot: this archive predates the database-dump format. "+
				"Re-capture with this DevKit version.\n")
		return localnet.ExitUserError
	}

	embedded, err := readEmbeddedState(tr)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return localnet.ExitUserError
	}
	if meta.SpliceVersion != embedded.SpliceVersion {
		_, _ = fmt.Fprintf(errw,
			"corrupt archive: snapshot.json reports Splice %s but embedded state.json reports %s\n",
			meta.SpliceVersion, embedded.SpliceVersion)
		return localnet.ExitUserError
	}

	_, _ = fmt.Fprintln(out, term.Prompt("", "", "", fmt.Sprintf(
		"dpm localnet restore %s %s %s",
		name, term.Amberc("--from"), src)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Validated header",
		fmt.Sprintf("schema %d · %d database(s) · captured %s",
			meta.SchemaVersion, meta.Database.DatabaseCount, meta.CreatedAt), ""))

	// The instance must already be registered — restore loads a dump into
	// the instance's EXISTING Postgres volume, and that volume only exists
	// (as a Docker-Compose-owned volume) after at least one `up`. Restoring
	// into a never-`up` instance would force the loader to create the volume
	// out of band (`docker run -v <vol>:...`), producing a volume Compose
	// does not own — which `down --volumes` / `remove --force` then refuse
	// to reclaim, stranding it. So require a prior `up` and never create a
	// volume here. (Includes cross-name restore: the target name must have
	// been `up` too.) See docs/limitations.md "Restore requires a prior up".
	//
	// Distinguish ErrNotFound from every other error so a permission/parse
	// failure can't be mistaken for "not registered".
	existing, rerr := registry.Read(name)
	if rerr != nil && !errors.Is(rerr, registry.ErrNotFound) {
		_, _ = fmt.Fprintf(errw, "read existing registry state for %q: %s\n", name, rerr)
		return localnet.ExitRuntimeFailure
	}
	if existing == nil {
		_, _ = fmt.Fprintf(errw,
			"instance %q not found — restore loads into an existing instance's "+
				"data volume. Run `localnet up %s` first, then restore.\n", name, name)
		return localnet.ExitUserError
	}
	if existing.Status == registry.StatusRunning {
		_, _ = fmt.Fprintf(errw,
			"instance %q is running — run `localnet down %s` first\n", name, name)
		return localnet.ExitUserError
	}
	if existing.SpliceVersion != meta.SpliceVersion && !force {
		_, _ = fmt.Fprintf(errw,
			"Splice version mismatch: existing instance is %s, snapshot is %s. "+
				"Pass --force to override.\n",
			existing.SpliceVersion, meta.SpliceVersion)
		return localnet.ExitUserError
	}

	// Build the registry record we'll commit only after a successful
	// load. On cross-name restore the compose-project / network /
	// container-prefix are rewritten to the target, matching up.go.
	toWrite := *embedded
	toWrite.Name = name
	toWrite.Status = registry.StatusStopped
	if embedded.Name != "" && embedded.Name != name {
		_, _ = fmt.Fprintln(errw, term.Warnc(fmt.Sprintf(
			"warning: snapshot's original instance name was %q; restoring as %q. "+
				"Embedded log paths and identifiers still reference the original name.",
			embedded.Name, name)))
		toWrite.ComposeProject = "canton-" + name
		toWrite.DockerNetwork = name
		toWrite.ContainerPrefix = name + "-"
	}

	// Hard guard: never start a second writer. If any container of the
	// target project is still up (a stale/half-down instance), refuse.
	if running, _ := archiverFn.AnyContainerRunning(ctx, toWrite.ComposeProject); running {
		_, _ = fmt.Fprintf(errw,
			"instance %q still has running containers — run `localnet down %s` first\n",
			name, name)
		return localnet.ExitUserError
	}

	// Disk preflight against Docker's volume store (where the restored
	// database lands), falling back to the host fs holding the archive.
	volAvail, volAvailErr := archiverFn.VolumeStorageAvailableBytes(ctx)
	if volAvailErr != nil {
		volAvail, _ = availableDiskBytes(filepath.Dir(src))
	}
	if volAvail > 0 {
		// A restored database is roughly the dump size; add a 50% margin
		// for indexes/WAL the load rebuilds.
		need := meta.Database.SizeBytes + meta.Database.SizeBytes/2
		if need > 0 && volAvail < uint64(need) {
			_, _ = fmt.Fprintf(errw,
				"insufficient disk space to restore: dump is ~%d MiB (needs ~%d MiB with margin) "+
					"but only %d MiB available on Docker's volume storage.\n",
				meta.Database.SizeBytes/1024/1024, need/1024/1024, volAvail/1024/1024)
			return localnet.ExitUserError
		}
	}

	// Find the dump entry and stream it into the restore loader while
	// re-hashing to verify ContentSHA.
	hdr, err := tr.Next()
	if err != nil {
		_, _ = fmt.Fprintf(errw, "read dump entry: %s\n", err)
		return localnet.ExitUserError
	}
	if !validArchiveDumpPath(hdr.Name) {
		_, _ = fmt.Fprintf(errw,
			"archive third entry must be %s (got %q)\n", archiveDumpName, hdr.Name)
		return localnet.ExitUserError
	}
	if hdr.Size > maxDump {
		_, _ = fmt.Fprintf(errw, "dump size %d exceeds %d-byte ceiling\n", hdr.Size, maxDump)
		return localnet.ExitRuntimeFailure
	}

	volume := toWrite.ComposeProject + "_" + meta.Database.VolumeSuffix
	_, _ = fmt.Fprintln(out, term.Step(term.StepBusy, "Restoring database", volume, ""))

	hasher := sha256.New()
	tee := io.TeeReader(io.LimitReader(tr, hdr.Size), hasher)
	if err := archiverFn.RestoreInto(ctx, meta.Database.PostgresImage, volume, meta.Database.User, tee); err != nil {
		_, _ = fmt.Fprintf(errw, "restore database: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	// Drain any trailing bytes so the hash covers the whole entry.
	_, _ = io.Copy(io.Discard, tee)
	got := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if meta.Database.ContentSHA != "" && got != meta.Database.ContentSHA {
		_, _ = fmt.Fprintf(errw,
			"dump SHA mismatch: archive reports %s, computed %s — restore may be incomplete\n",
			meta.Database.ContentSHA, got)
		return localnet.ExitRuntimeFailure
	}

	// Commit the registry only after a successful load.
	if err := registry.Write(&toWrite); err != nil {
		_, _ = fmt.Fprintf(errw, "register restored instance: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Restored database", volume, ""))
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, term.Box(term.BoxBrand,
		fmt.Sprintf("%s Restored %s from %s (%d database(s))\n%s",
			term.Brandc("✦"),
			term.Bold("\""+name+"\""),
			term.Bold(src),
			meta.Database.DatabaseCount,
			term.Dimc(fmt.Sprintf("Run %s to bring it up.",
				term.Textc(fmt.Sprintf("localnet up %s", name)))))))
	return localnet.ExitSuccess
}

// readSnapshotHeader pulls the FIRST tar entry (snapshot.json).
func readSnapshotHeader(tr *tar.Reader) (*types.Snapshot, error) {
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if hdr.Name != archiveHeaderName {
		return nil, fmt.Errorf("archive does not start with %s (got %q)", archiveHeaderName, hdr.Name)
	}
	if hdr.Size > 1<<20 {
		return nil, fmt.Errorf("header oversized: %d bytes", hdr.Size)
	}
	buf, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
	if err != nil {
		return nil, fmt.Errorf("read header body: %w", err)
	}
	var meta types.Snapshot
	if err := json.Unmarshal(buf, &meta); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	return &meta, nil
}

// readEmbeddedState pulls the SECOND tar entry (state.json).
func readEmbeddedState(tr *tar.Reader) (*registry.State, error) {
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("read state entry: %w", err)
	}
	if hdr.Name != archiveStateName {
		return nil, fmt.Errorf("archive second entry must be %s (got %q)", archiveStateName, hdr.Name)
	}
	if hdr.Size > 1<<20 {
		return nil, fmt.Errorf("embedded state.json oversized: %d bytes", hdr.Size)
	}
	buf, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
	if err != nil {
		return nil, fmt.Errorf("read state body: %w", err)
	}
	var s registry.State
	if err := json.Unmarshal(buf, &s); err != nil {
		return nil, fmt.Errorf("parse embedded state.json: %w", err)
	}
	return &s, nil
}

// validArchiveDumpPath is the Zip Slip gate: the dump entry must be
// exactly archiveDumpName, nothing else.
func validArchiveDumpPath(name string) bool {
	return name == archiveDumpName && path.Clean(name) == name
}

// streamTarEntry copies bodyPath into tw as `name`. `size` is trusted
// (the caller just wrote bodyPath) to avoid a TOCTOU stat.
func streamTarEntry(tw *tar.Writer, name, bodyPath string, size int64) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Size: size, Mode: 0o600, ModTime: time.Now().UTC(),
	}); err != nil {
		return err
	}
	f, err := os.Open(bodyPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(tw, f)
	return err
}

// writeTarEntry is the small-body sibling — header + state.json.
func writeTarEntry(tw *tar.Writer, name string, body []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Size: int64(len(body)), Mode: 0o600, ModTime: time.Now().UTC(),
	}); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

// cappedWriter enforces a max byte ceiling so a runaway dump can't fill
// the staging disk.
type cappedWriter struct {
	inner    io.Writer
	max      int64
	written  int64
	exceeded bool
}

func newCappedWriter(w io.Writer, maxBytes int64) *cappedWriter {
	return &cappedWriter{inner: w, max: maxBytes}
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.exceeded || c.written+int64(len(p)) > c.max {
		c.exceeded = true
		return 0, fmt.Errorf("entry size ceiling %d bytes exceeded", c.max)
	}
	n, err := c.inner.Write(p)
	c.written += int64(n)
	return n, err
}

// dockerPgArchiver — production pgArchiver. Talks to docker / postgres
// via the docker CLI.
type dockerPgArchiver struct{}

func (dockerPgArchiver) ResolvePostgres(ctx context.Context, composeProject string) (pgInfo, error) {
	name, err := exec.CommandContext(ctx, "docker", "ps",
		"--filter", "label=com.docker.compose.project="+composeProject,
		"--filter", "label=com.docker.compose.service=postgres",
		"--format", "{{.Names}}").Output()
	container := strings.TrimSpace(string(err2nil(name, err)))
	if container == "" {
		return pgInfo{}, fmt.Errorf("no running postgres container for project %q", composeProject)
	}
	// First line only, in case of replicas.
	if i := strings.IndexByte(container, '\n'); i >= 0 {
		container = container[:i]
	}

	user := dockerExecOut(ctx, container, "printenv", "POSTGRES_USER")
	if user == "" {
		user = "postgres"
	}
	image := strings.TrimSpace(string(err2nil(
		exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.Config.Image}}", container).Output())))

	volume := strings.TrimSpace(string(err2nil(exec.CommandContext(ctx, "docker", "inspect", "-f",
		`{{range .Mounts}}{{if eq .Destination "`+pgDataDir+`"}}{{.Name}}{{end}}{{end}}`, container).Output())))
	if volume == "" {
		volume = composeProject + "_postgres"
	}
	suffix := strings.TrimPrefix(volume, composeProject+"_")

	return pgInfo{Container: container, User: user, Image: image, VolumeSuffix: suffix}, nil
}

func (dockerPgArchiver) AnyContainerRunning(ctx context.Context, composeProject string) (bool, error) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q",
		"--filter", "label=com.docker.compose.project="+composeProject).Output()
	if err != nil {
		return false, fmt.Errorf("docker ps: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func (dockerPgArchiver) Quiesce(ctx context.Context, composeProject, pgContainer string) (func(), error) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Names}}",
		"--filter", "label=com.docker.compose.project="+composeProject).Output()
	if err != nil {
		return func() {}, fmt.Errorf("list containers: %w", err)
	}
	var nodes []string
	for _, n := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if n = strings.TrimSpace(n); n != "" && n != pgContainer {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return func() {}, nil
	}
	// Freeze the node processes (SIGSTOP) so nothing writes to Postgres
	// during the dump. Postgres itself is left running so pg_dumpall can
	// read. Pause is instant and keeps ports bound — no reboot cost.
	if err := exec.CommandContext(ctx, "docker", append([]string{"pause"}, nodes...)...).Run(); err != nil {
		_ = exec.Command("docker", append([]string{"unpause"}, nodes...)...).Run()
		return func() {}, fmt.Errorf("pause node containers: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// Detached from ctx: we must unpause even if the dump's context
			// was cancelled (e.g. a timeout), or the instance is left frozen.
			_ = exec.Command("docker", append([]string{"unpause"}, nodes...)...).Run()
		})
	}, nil
}

func (dockerPgArchiver) DumpAll(ctx context.Context, container, user string, w io.Writer) error {
	var errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "exec", container,
		"pg_dumpall", "-U", user, "--clean", "--if-exists")
	cmd.Stdout = w
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dumpall: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func (dockerPgArchiver) CountDatabases(ctx context.Context, container, user string) int {
	out := dockerExecOut(ctx, container, "psql", "-U", user, "-d", "postgres", "-At", "-c",
		"select count(*) from pg_database where datname not in ('template0','template1','postgres')")
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func (dockerPgArchiver) RestoreInto(ctx context.Context, image, volume, user string, r io.Reader) error {
	if image == "" || volume == "" || user == "" {
		return fmt.Errorf("incomplete restore target (image=%q volume=%q user=%q)", image, volume, user)
	}
	tmpName := "dpm-restore-pg-" + sanitizeContainerName(volume)
	// Clear any leftover from a previous interrupted restore.
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", tmpName).Run()

	// Throwaway Postgres on the target volume. RunRestore guarantees the
	// volume already exists (Compose created it on a prior `up`), so this
	// never brings a volume into being out of band. HOST_AUTH_METHOD=trust
	// is kept as a belt-and-braces: if the volume's data dir is somehow
	// uninitialised, Postgres initialises without a password and accepts
	// the local psql connection; for an already-initialised volume (the
	// normal case) the env is ignored.
	if err := exec.CommandContext(ctx, "docker", "run", "-d", "--name", tmpName,
		"-v", volume+":"+pgDataDir,
		"-e", "POSTGRES_USER="+user,
		"-e", "POSTGRES_HOST_AUTH_METHOD=trust",
		image).Run(); err != nil {
		return fmt.Errorf("start restore container: %w", err)
	}
	defer func() { _ = exec.Command("docker", "rm", "-f", tmpName).Run() }()

	ready := false
	for i := 0; i < 60; i++ {
		if exec.CommandContext(ctx, "docker", "exec", tmpName, "pg_isready", "-U", user).Run() == nil {
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if !ready {
		return fmt.Errorf("restore Postgres did not become ready")
	}

	// Load the dump. ON_ERROR_STOP=0 because pg_dumpall --clean emits a
	// DROP/CREATE for the connection role, which deterministically fails
	// (a role can't drop itself / already exists); we scan stderr and
	// fail only on errors outside that allowlist.
	var errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", tmpName,
		"psql", "-U", user, "-d", "postgres", "-v", "ON_ERROR_STOP=0")
	cmd.Stdin = r
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql restore: %w: %s", err, firstLines(errBuf.String(), 3))
	}
	if bad := nonIgnorableRestoreErrors(errBuf.String(), user); len(bad) > 0 {
		return fmt.Errorf("psql restore reported %d error(s), e.g.: %s",
			len(bad), strings.Join(bad[:min(len(bad), 3)], " | "))
	}
	return nil
}

// nonIgnorableRestoreErrors returns the psql ERROR/FATAL lines that are
// NOT the two deterministic connection-role lines a `pg_dumpall --clean`
// restore always emits.
func nonIgnorableRestoreErrors(stderr, user string) []string {
	roleExists := `role "` + user + `" already exists`
	var bad []string
	sc := bufio.NewScanner(strings.NewReader(stderr))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "ERROR") && !strings.Contains(line, "FATAL") {
			continue
		}
		if strings.Contains(line, "current user cannot be dropped") ||
			strings.Contains(line, roleExists) {
			continue
		}
		bad = append(bad, strings.TrimSpace(line))
	}
	return bad
}

func (dockerPgArchiver) VolumeStorageAvailableBytes(ctx context.Context) (uint64, error) {
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", "/probe",
		"alpine:3.20", "df", "-Pk", "/probe").Output()
	if err != nil {
		return 0, fmt.Errorf("probe docker volume storage: %w", err)
	}
	return parseDfAvailableKB(string(out))
}

// parseDfAvailableKB extracts the Available column (1K-blocks) from
// POSIX `df -Pk` output and returns it as bytes.
func parseDfAvailableKB(dfOut string) (uint64, error) {
	for _, line := range strings.Split(strings.TrimSpace(dfOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		availKB, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			continue
		}
		return availKB * 1024, nil
	}
	return 0, fmt.Errorf("df output not parseable: %q", dfOut)
}

// dockerExecOut runs `docker exec <container> <args...>` and returns
// trimmed stdout, or "" on error.
func dockerExecOut(ctx context.Context, container string, args ...string) string {
	full := append([]string{"exec", container}, args...)
	out, err := exec.CommandContext(ctx, "docker", full...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sanitizeContainerName keeps a docker-safe suffix for the throwaway
// container name.
func sanitizeContainerName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

func err2nil(out []byte, err error) []byte {
	if err != nil {
		return nil
	}
	return out
}
