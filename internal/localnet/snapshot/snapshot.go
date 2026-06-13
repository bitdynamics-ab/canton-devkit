package snapshot

import (
	"archive/tar"
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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

// Implements `dpm localnet snapshot` + `restore`. Key safety
// properties:
//
// 1. Zip Slip on restore is rejected: validateArchivePath only
// accepts strictly `volumes/<safename>.tar` and validateVolumeName
// constrains the inner component.
// 2. Nothing is buffered whole in RAM: volumes stream straight into
// tar.Writer; restore reads streaming via io.Pipe.
// 3. registry.State is captured as the SECOND archive entry so a
// restored snapshot is bringable; restore re-registers via
// registry.Write.
// 4. Content is verified: streaming sha256 on both writer + reader;
// mismatch aborts with the volume name.
// 5. Splice-version mismatch is refused (unless --force):
// header.SpliceVersion is compared to the existing instance or the
// embedded state.json.
//
// On-disk layout (strict):
//
// snapshot.tgz
// ├── snapshot.json (types.Snapshot header, FIRST)
// ├── state.json (registry.State, SECOND)
// ├── volumes/<vol>.tar (one per docker volume; <vol> must
// │ validate via validateVolumeName)
// └── …

const (
	snapshotSchemaVersion = 1

	archiveHeaderName  = "snapshot.json"
	archiveStateName   = "state.json"
	archiveVolumesDir  = "volumes"
	archiveVolumesPath = archiveVolumesDir + "/"

	// maxArchiveEntry caps any single entry so a malicious header
	// advertising 2^63 bytes can't OOM the reader or fill the
	// staging disk. 16 GiB > any reasonable Splice LocalNet volume.
	maxArchiveEntry = 16 << 30
)

// volumeArchiver is the seam tests use; production uses
// dockerVolumeArchiver. All three methods are streaming so neither
// side ever holds a whole volume in memory.
//
// RestoreVolume takes the target compose project so it can re-apply
// the `com.docker.compose.project` / `com.docker.compose.volume`
// labels Compose itself stamps on `up`. Compose does NOT relabel a
// pre-existing volume — so without this, a restored volume is
// invisible to ListVolumes' label filter and every later snapshot of
// the restored instance silently captures zero volumes.
type volumeArchiver interface {
	ListVolumes(ctx context.Context, composeProject string) ([]string, error)
	ArchiveVolume(ctx context.Context, volume string, w io.Writer) error
	RestoreVolume(ctx context.Context, composeProject, volume string, r io.Reader) error
	// VolumeStorageAvailableBytes reports the free space on the
	// filesystem Docker extracts volumes into (its data root), NOT the
	// host filesystem holding the snapshot archive. On macOS/Windows
	// the volume store lives inside the Docker VM's disk image, which a
	// host statfs cannot see — so the restore preflight must ask Docker.
	VolumeStorageAvailableBytes(ctx context.Context) (uint64, error)
}

var archiverFn volumeArchiver = dockerVolumeArchiver{}

// SetArchiverForTest swaps the docker-backed archiver for a fake and
// returns a restore func. It exists so callers in OTHER packages —
// notably the Web UI snapshot handler tests — can exercise the
// snapshot/restore happy path without a docker daemon, the same way
// installFakeArchiver does for this package's own tests. Production
// code never calls this; the in-package tests use the unexported
// installFakeArchiver helper instead.
func SetArchiverForTest(a volumeArchiver) (restore func()) {
	prev := archiverFn
	archiverFn = a
	return func() { archiverFn = prev }
}

// RunSnapshot streams a snapshot to dest. Memory footprint per
// volume is bounded by the tar copy buffer, not the volume size.
// Two-pass strategy: write each volume body to an intermediate tar
// file (so we know its size + SHA when writing the header), then
// copy header + state.json + intermediate into the real archive in
// strict on-disk order.
func RunSnapshot(ctx context.Context, out io.Writer, errw io.Writer, name, dest string) int {
	// Per-instance exclusive lock. Snapshot reads volumes via
	// temporary `docker run` containers and
	// concurrent execution against the same instance can produce a
	// half-snapshotted archive. The lock is the SAME one held by
	// `localnet up`/`down`, so a snapshot while the instance is
	// being torn down will refuse with "instance busy" — exactly
	// the behaviour we want.
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

	// Application-consistency caveat. Snapshotting a RUNNING
	// instance copies its Docker volumes live: it captures a
	// crash-consistent point-in-time, not an application-consistent one.
	// In-flight ledger transactions or unflushed Postgres/Canton writes
	// may be only partially present, so a restored copy can need crash
	// recovery (or, rarely, be inconsistent). For a guaranteed-consistent
	// snapshot, `localnet pause` the instance first.
	//
	// The wording is shared with the Web UI via RunningSnapshotWarning
	// so both surfaces tell the operator the same thing (CLI↔UI
	// parity): the CLI prints it as a StepWarn here, the snapshot
	// handler echoes it in a response header.
	if state.Status == registry.StatusRunning {
		_, _ = fmt.Fprintln(errw, term.Step(term.StepWarn,
			"Snapshotting a running instance", RunningSnapshotWarning(name), ""))
	}

	volumes, err := archiverFn.ListVolumes(ctx, state.ComposeProject)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "list volumes for %q: %s\n", state.ComposeProject, err)
		return localnet.ExitRuntimeFailure
	}
	sort.Strings(volumes)
	for _, v := range volumes {
		if err := validateVolumeName(v); err != nil {
			_, _ = fmt.Fprintf(errw, "volume %q rejected: %s\n", v, err)
			return localnet.ExitRuntimeFailure
		}
	}

	// Empty-volume guard. ListVolumes filters on the
	// com.docker.compose.project label; an empty result almost always
	// means the instance has never been brought up, or its volumes
	// lost their Compose labels (the classic symptom of a restore that
	// didn't relabel). Either way, writing a header+state-only archive
	// and reporting "0 volume(s)" success would silently discard all
	// data on a snapshot→restore→snapshot round-trip. Refuse so the
	// failure is loud at capture time rather than at the next restore.
	if len(volumes) == 0 {
		_, _ = fmt.Fprintln(errw, term.Step(term.StepWarn,
			"No docker volumes found for "+state.ComposeProject,
			"nothing to capture — has the instance been brought up? "+
				"(a restored instance whose volumes lost their Compose "+
				"labels also shows up empty)", ""))
		_, _ = fmt.Fprintf(errw,
			"refusing to write an empty snapshot of %q\n", name)
		return localnet.ExitUserError
	}

	_, _ = fmt.Fprintln(out, term.Prompt("", "", "", fmt.Sprintf(
		"dpm localnet snapshot %s %s %s %s",
		term.Amberc("--name"), name, term.Amberc("--to"), dest)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Reading registry state", state.ComposeProject, ""))
	_, _ = fmt.Fprintf(out, "%s\n", term.Step(term.StepCheck,
		fmt.Sprintf("Listed %d volume(s)", len(volumes)), strings.Join(volumes, ", "), ""))

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

	intermediate, err := os.CreateTemp(filepath.Dir(tmp), "snap-vols-*.tar")
	if err != nil {
		_ = f.Close()
		_, _ = fmt.Fprintf(errw, "create intermediate tar: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	intermediatePath := intermediate.Name()
	defer func() { _ = os.Remove(intermediatePath) }()

	itw := tar.NewWriter(intermediate)
	header := types.Snapshot{
		SchemaVersion: snapshotSchemaVersion,
		Instance:      name,
		SpliceVersion: state.SpliceVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		DevKitVersion: "dev",
	}
	for _, vol := range volumes {
		_, _ = fmt.Fprintln(out, term.Step(term.StepBusy, "Capturing volume", vol, ""))
		volPath, written, sum, err := streamVolumeToTemp(ctx, vol, filepath.Dir(tmp))
		if err != nil {
			_ = itw.Close()
			_ = intermediate.Close()
			_ = f.Close()
			_, _ = fmt.Fprintf(errw, "archive %q: %s\n", vol, err)
			return localnet.ExitRuntimeFailure
		}
		header.Volumes = append(header.Volumes, types.SnapshotVolume{
			Name:       vol,
			SizeBytes:  written,
			ContentSHA: sum,
		})
		if err := streamTarEntry(itw, path.Join(archiveVolumesDir, vol+".tar"),
			volPath, written); err != nil {
			_ = os.Remove(volPath)
			_, _ = fmt.Fprintf(errw, "stream %q: %s\n", vol, err)
			return localnet.ExitRuntimeFailure
		}
		_ = os.Remove(volPath)
	}
	if err := itw.Close(); err != nil {
		_, _ = fmt.Fprintf(errw, "close intermediate tar: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	if _, err := intermediate.Seek(0, io.SeekStart); err != nil {
		_, _ = fmt.Fprintf(errw, "rewind intermediate: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	// On-disk order: header → state.json → volumes/*. Reader
	// depends on it.
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
	itr := tar.NewReader(intermediate)
	for {
		ih, err := itr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_, _ = fmt.Fprintf(errw, "read intermediate: %s\n", err)
			return localnet.ExitRuntimeFailure
		}
		if err := tw.WriteHeader(ih); err != nil {
			_, _ = fmt.Fprintf(errw, "write entry header %q: %s\n", ih.Name, err)
			return localnet.ExitRuntimeFailure
		}
		if _, err := io.Copy(tw, itr); err != nil {
			_, _ = fmt.Fprintf(errw, "copy %q: %s\n", ih.Name, err)
			return localnet.ExitRuntimeFailure
		}
	}
	_ = intermediate.Close()

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

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, term.Box(term.BoxBrand,
		fmt.Sprintf("%s Snapshot of %s saved to %s\n%s",
			term.Brandc("✦"),
			term.Bold("\""+name+"\""),
			term.Bold(dest),
			term.Dimc(fmt.Sprintf("%d volume(s) · %s · run %s to restore.",
				len(volumes), header.CreatedAt,
				term.Textc(fmt.Sprintf("localnet restore --name %s --from %s", name, dest)))))))
	return localnet.ExitSuccess
}

// RunningSnapshotWarning is the canonical plain-text caveat shown when
// a snapshot is taken of a RUNNING instance. It lives here, not at the
// call sites, so the CLI (RunSnapshot, as a StepWarn) and the Web UI
// (the snapshot handler's X-Snapshot-Warning response header) emit
// identical wording — CLI↔UI parity for the same guard. The plain
// string (no ANSI) is what HTTP surfaces carry.
func RunningSnapshotWarning(name string) string {
	return "crash-consistent only — in-flight writes may be partial; " +
		"`localnet pause --name " + name + "` first for an application-consistent copy"
}

// streamVolumeToTemp runs ArchiveVolume into a temp file under
// dir, computing sha256 and a size ceiling as it goes. Returns
// (path, written, sha) on success. Caller owns the temp file's
// lifecycle (os.Remove) and must close any tw handle even on
// error — we never touch tw here.
func streamVolumeToTemp(ctx context.Context, vol, dir string) (string, int64, string, error) {
	volFile, err := os.CreateTemp(dir, "snap-vol-*.tar")
	if err != nil {
		return "", 0, "", fmt.Errorf("create vol tmp: %w", err)
	}
	volPath := volFile.Name()
	hasher := sha256.New()
	capW := newCappedWriter(volFile, maxArchiveEntry)
	mw := io.MultiWriter(capW, hasher)
	if err := archiverFn.ArchiveVolume(ctx, vol, mw); err != nil {
		_ = volFile.Close()
		_ = os.Remove(volPath)
		return "", 0, "", err
	}
	if capW.exceeded {
		_ = volFile.Close()
		_ = os.Remove(volPath)
		return "", 0, "", fmt.Errorf("volume exceeds %d-byte ceiling", maxArchiveEntry)
	}
	if err := volFile.Close(); err != nil {
		_ = os.Remove(volPath)
		return "", 0, "", fmt.Errorf("close vol tmp: %w", err)
	}
	return volPath, capW.written, "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// RunRestore streams the archive: validates header, refuses on
// version mismatch (unless --force), refuses on existing running
// instance, re-registers from embedded state.json, restores each
// volume while re-hashing to verify ContentSHA.
func RunRestore(ctx context.Context, out io.Writer, errw io.Writer, name, src string, force bool) int {
	// Per-instance exclusive lock. Without this,
	// `dpm localnet restore --name X` and `POST /api/instances/X/
	// snapshot/restore` can run simultaneously and half-merge two
	// volume sets, with the registry pointing at whichever Write
	// wins the race.
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
	expectedSHA := make(map[string]string, len(meta.Volumes))
	for _, v := range meta.Volumes {
		expectedSHA[v.Name] = v.ContentSHA
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
		"dpm localnet restore %s %s %s %s",
		term.Amberc("--name"), name, term.Amberc("--from"), src)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Validated header",
		fmt.Sprintf("schema %d · %d volume(s) · captured %s",
			meta.SchemaVersion, len(meta.Volumes), meta.CreatedAt), ""))

	// Distinguish ErrNotFound (the legitimate "no existing instance"
	// case) from every other error. A bare `existing, _ :=
	// registry.Read(name)` would swallow permission-denied and corrupt
	// JSON, then assume nil meant "no existing instance" — masking real
	// failures and letting a restore overwrite a state file. Surface
	// any non-ErrNotFound error and bail.
	existing, rerr := registry.Read(name)
	if rerr != nil && !errors.Is(rerr, registry.ErrNotFound) {
		_, _ = fmt.Fprintf(errw, "read existing registry state for %q: %s\n", name, rerr)
		return localnet.ExitRuntimeFailure
	}
	if existing != nil {
		if existing.Status == registry.StatusRunning {
			_, _ = fmt.Fprintf(errw,
				"instance %q is running — run `localnet down --name %s` first\n", name, name)
			return localnet.ExitUserError
		}
		if existing.SpliceVersion != meta.SpliceVersion && !force {
			_, _ = fmt.Fprintf(errw,
				"Splice version mismatch: existing instance is %s, snapshot is %s. "+
					"Restore would write volumes formatted for a different binary. "+
					"Pass --force to override.\n",
				existing.SpliceVersion, meta.SpliceVersion)
			return localnet.ExitUserError
		}
	}

	// If the user-supplied --name differs from the snapshot's embedded
	// original name, surface a warning. The restore proceeds
	// (renaming-on-restore is a
	// supported workflow) but the user should know they're not
	// recovering the original identity — e.g. agents, log paths,
	// and credentials baked into the embedded state may reference
	// the OLD name. This is a soft warning, NOT a refusal.
	if embedded.Name != "" && embedded.Name != name {
		_, _ = fmt.Fprintln(errw, term.Warnc(fmt.Sprintf(
			"warning: snapshot's original instance name was %q; restoring as %q. "+
				"Embedded log paths and identifiers still reference the original name.",
			embedded.Name, name)))
	}

	// Disk preflight. Restore unpacks every volume tar into Docker's
	// volume store, NOT the filesystem holding the snapshot archive
	// (src). Those are routinely different filesystems — and on
	// macOS/Windows the volume store lives inside the Docker VM's disk
	// image, invisible to a host statfs of src's directory. Measuring
	// the wrong one lets the gate both false-refuse (tiny /tmp, huge
	// Docker disk) and false-pass (roomy home, full Docker VM), the
	// latter reintroducing the half-populated-volume-on-ENOSPC failure
	// this preflight exists to prevent. So ask Docker. The host check
	// on filepath.Dir(src) is kept only as a secondary fallback (and
	// as a bound on the spooled archive itself) when the Docker probe
	// is unavailable.
	volAvail, volAvailErr := archiverFn.VolumeStorageAvailableBytes(ctx)
	if volAvailErr != nil {
		// Probe failed (old docker, no alpine image, daemon hiccup):
		// fall back to the host filesystem holding the archive. Better
		// an approximate gate than none.
		volAvail, _ = availableDiskBytes(filepath.Dir(src))
	}
	if volAvail > 0 {
		var need int64
		for _, v := range meta.Volumes {
			need += v.SizeBytes
		}
		needWithMargin := need + need/5 // 20%
		if needWithMargin > 0 && volAvail < uint64(needWithMargin) {
			_, _ = fmt.Fprintf(errw,
				"insufficient disk space to restore: snapshot needs ~%d MiB (with margin) but only %d MiB available on Docker's volume storage. "+
					"Free space and retry.\n",
				needWithMargin/1024/1024, volAvail/1024/1024)
			return localnet.ExitUserError
		}
	}
	// A zero/unknown availability (probe AND host statfs both failed,
	// e.g. unsupported FS or permissions) is non-fatal: we proceed
	// without the preflight rather than blocking restore. The unpack
	// loop will still surface ENOSPC with a useful error.

	// Build the registry record we WOULD write — but defer the actual
	// Write until after all volumes restore successfully. Writing the
	// registry first and unpacking volumes after would leave a registry
	// entry pointing at a half-restored instance with no rollback on a
	// volume failure. Either commit the registry on full success or
	// leave it untouched.
	//
	// When --name differs from the embedded original, the
	// compose-project / network / container-prefix fields must be
	// rewritten too, matching the naming convention in
	// internal/localnet/up.go: ComposeProject = "canton-"+name,
	// DockerNetwork = name, ContainerPrefix = name+"-".
	toWrite := *embedded
	toWrite.Name = name
	toWrite.Status = registry.StatusStopped
	srcComposeProject := embedded.ComposeProject
	if embedded.Name != "" && embedded.Name != name {
		toWrite.ComposeProject = "canton-" + name
		toWrite.DockerNetwork = name
		toWrite.ContainerPrefix = name + "-"
	}

	// Cumulative tar-size budget. The per-entry maxArchiveEntry cap
	// (16 GiB) doesn't bound N×16 GiB
	// across the archive. Track extracted bytes across the loop and
	// abort if we'd exceed the budget. Reuse the Docker volume-storage
	// availability computed for the size preflight above — that's the
	// filesystem the bytes actually land on. volAvail==0 means both
	// the Docker probe and the host fallback failed; we then fall back
	// to a hard 256 GiB cumulative ceiling — well above any reasonable
	// LocalNet snapshot but a backstop against a hostile archive with
	// under-reported SizeBytes.
	const cumulativeFloor = int64(256) << 30 // 256 GiB
	cumulativeBudget := cumulativeFloor
	if volAvail > 0 {
		// Leave a 20% margin to avoid filling the filesystem.
		margin := uint64(float64(volAvail) * 0.8)
		if margin > 0 && int64(margin) < cumulativeBudget {
			cumulativeBudget = int64(margin)
		}
	}
	var cumulativeRestored int64

	restored := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_, _ = fmt.Fprintf(errw, "read entry: %s\n", err)
			return localnet.ExitRuntimeFailure
		}
		srcVolName, ok := validateArchivePath(hdr.Name)
		if !ok {
			_, _ = fmt.Fprintln(out, term.Step(term.StepWarn,
				"Skipping unknown entry", hdr.Name, ""))
			if _, err := io.CopyN(io.Discard, tr, hdr.Size); err != nil && err != io.EOF {
				_, _ = fmt.Fprintf(errw, "discard %q: %s\n", hdr.Name, err)
				return localnet.ExitRuntimeFailure
			}
			continue
		}
		// rewrite the volume name so the restored docker
		// volume sits under the target instance's compose-project
		// prefix. When --name matches the embedded original this is
		// a no-op (srcComposeProject == toWrite.ComposeProject) and
		// the volume keeps its original name — same-name restore is
		// unchanged. For cross-name, the rewrite is what lets two
		// instances (source + clone) coexist with independent volumes.
		dstVolName := rewriteVolumeForTarget(
			srcVolName, srcComposeProject, toWrite.ComposeProject)
		if hdr.Size > maxArchiveEntry {
			_, _ = fmt.Fprintf(errw, "volume %q size %d exceeds %d-byte ceiling\n",
				dstVolName, hdr.Size, maxArchiveEntry)
			return localnet.ExitRuntimeFailure
		}
		// Cumulative budget check BEFORE extracting, using hdr.Size
		// (the tar-declared size); the streaming
		// copy is also bounded to hdr.Size via io.LimitReader, so a
		// rogue archive cannot blow past this number even with a
		// truncated header.
		if cumulativeRestored+hdr.Size > cumulativeBudget {
			_, _ = fmt.Fprintf(errw,
				"cumulative restore size %d + this volume %d would exceed budget %d "+
					"(remaining disk minus 20%% margin); aborting before disk fills\n",
				cumulativeRestored, hdr.Size, cumulativeBudget)
			return localnet.ExitRuntimeFailure
		}
		_, _ = fmt.Fprintln(out, term.Step(term.StepBusy, "Restoring volume", dstVolName, ""))

		// Tee into archiver + sha hasher via io.Pipe so we
		// stream without buffering. The goroutine writes; the
		// archiver reads.
		hasher := sha256.New()
		pr, pw := io.Pipe()
		copyErr := make(chan error, 1)
		go func() {
			_, err := io.Copy(io.MultiWriter(pw, hasher), io.LimitReader(tr, hdr.Size))
			_ = pw.CloseWithError(err)
			copyErr <- err
		}()
		restErr := archiverFn.RestoreVolume(ctx, toWrite.ComposeProject, dstVolName, pr)
		// If the alpine `tar xf -` exited early (e.g. disk full
		// mid-stream), pr's reader side is closed but
		// the goroutine on the other end may still be blocked
		// writing into the pipe buffer. Closing pr with the error
		// unblocks the writer so we don't leak a goroutine on the
		// error path. (Successful path: io.Copy returned EOF, the
		// goroutine already exited via CloseWithError(nil).)
		if restErr != nil {
			_ = pr.CloseWithError(restErr)
		}
		streamErr := <-copyErr
		if restErr != nil {
			_, _ = fmt.Fprintf(errw, "restore %q: %s\n", dstVolName, restErr)
			return localnet.ExitRuntimeFailure
		}
		if streamErr != nil && streamErr != io.EOF {
			_, _ = fmt.Fprintf(errw, "stream %q: %s\n", dstVolName, streamErr)
			return localnet.ExitRuntimeFailure
		}
		got := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
		// SHA was computed on the SOURCE name at snapshot time;
		// look it up that way even when we restored under a new
		// name. Bytes are identical; only the docker label changed.
		want, hadExpected := expectedSHA[srcVolName]
		if hadExpected && got != want {
			_, _ = fmt.Fprintf(errw,
				"volume %q SHA mismatch: archive reports %s, computed %s\n",
				dstVolName, want, got)
			return localnet.ExitRuntimeFailure
		}
		restored++
		cumulativeRestored += hdr.Size
		_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Restored volume", dstVolName, ""))
	}

	// Commit point: only NOW do we write the registry. Up to here any
	// failure leaves the existing registry entry (if
	// any) and the volumes we did unpack in place — but no
	// half-committed registry row claiming an instance is restored
	// when it isn't.
	if err := registry.Write(&toWrite); err != nil {
		_, _ = fmt.Fprintf(errw, "register restored instance: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, term.Box(term.BoxBrand,
		fmt.Sprintf("%s Restored %s from %s (%d volume(s))\n%s",
			term.Brandc("✦"),
			term.Bold("\""+name+"\""),
			term.Bold(src),
			restored,
			term.Dimc(fmt.Sprintf("Run %s to bring it up.",
				term.Textc(fmt.Sprintf("localnet up --name %s", name)))))))
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

// validateArchivePath is the Zip Slip gate. Rejects anything that
// isn't strictly
// `volumes/<safename>.tar`. Returns the extracted volume name on
// success.
func validateArchivePath(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if path.Clean(name) != name {
		return "", false
	}
	if !strings.HasPrefix(name, archiveVolumesPath) || !strings.HasSuffix(name, ".tar") {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(name, archiveVolumesPath), ".tar")
	if inner == "" || strings.Contains(inner, "/") {
		return "", false
	}
	if err := validateVolumeName(inner); err != nil {
		return "", false
	}
	return inner, true
}

// validateVolumeName mirrors docker's own volume-name regex
// (^[a-zA-Z0-9][a-zA-Z0-9_.-]*$, max 64). Tightening here doubles
// as the Zip Slip safety net.
func validateVolumeName(s string) error {
	if s == "" || len(s) > 64 {
		return fmt.Errorf("length out of range")
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			// ok
		case i > 0 && (r == '_' || r == '.' || r == '-'):
			// ok — docker forbids these as first char
		default:
			return fmt.Errorf("disallowed char %q at %d", r, i)
		}
	}
	return nil
}

// rewriteVolumeForTarget translates a captured volume name from its
// source compose-project prefix to the target's, when --name differs
// from the embedded original. Docker compose volumes are named
// `<project>_<suffix>`; on cross-name restore we want the same suffix
// under the new project so the restored instance has independent
// volumes.
//
// Examples (src="canton-pebble", dst="canton-pebble-clone"):
//
//	canton-pebble_postgres -> canton-pebble-clone_postgres
//	canton-pebble_domain-upgrade-dump -> canton-pebble-clone_domain-upgrade-dump
//
// Edge cases:
//
// - src == dst (same-name restore): returns name unchanged. The
// common path stays a no-op so the no-rewrite branch can't drift.
// - name doesn't start with src+"_": returns name unchanged with a
// conservative bias. This shouldn't happen for archives produced
// by our snapshot path (every volume there carries the source's
// compose-project prefix) but a custom or hand-crafted archive
// might. Leaving the name alone is safer than producing a
// nonsense rewrite the operator can't diagnose.
// - src == "": returns name unchanged. The embedded state.json
// should always carry ComposeProject, but defending against an
// older snapshot is cheap.
func rewriteVolumeForTarget(name, srcProject, dstProject string) string {
	if srcProject == "" || srcProject == dstProject {
		return name
	}
	prefix := srcProject + "_"
	if !strings.HasPrefix(name, prefix) {
		return name
	}
	return dstProject + "_" + name[len(prefix):]
}

// streamTarEntry copies bodyPath into tw as `name`. `size` is
// trusted (passed by caller who just wrote bodyPath) to avoid a
// TOCTOU stat.
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

// writeTarEntry is the small-body sibling — header + state.json,
// both < 1 MiB.
func writeTarEntry(tw *tar.Writer, name string, body []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Size: int64(len(body)), Mode: 0o600, ModTime: time.Now().UTC(),
	}); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

// cappedWriter enforces a max byte ceiling. Used during snapshot
// so a runaway tar can't fill the staging disk.
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

// dockerVolumeArchiver — production volumeArchiver. Streaming via
// docker run -i / -v.
type dockerVolumeArchiver struct{}

func (dockerVolumeArchiver) ListVolumes(ctx context.Context, composeProject string) ([]string, error) {
	args := []string{
		"volume", "ls",
		"--filter", "label=com.docker.compose.project=" + composeProject,
		"--format", "{{.Name}}",
	}
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	var vols []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			vols = append(vols, line)
		}
	}
	return vols, nil
}

func (dockerVolumeArchiver) ArchiveVolume(ctx context.Context, volume string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", volume+":/src:ro",
		"alpine:3.20", "tar", "cf", "-", "-C", "/src", ".")
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (dockerVolumeArchiver) RestoreVolume(ctx context.Context, composeProject, volume string, r io.Reader) error {
	// Re-stamp the Compose labels Compose itself applies on `up`.
	// `docker volume create` is a no-op when the volume already
	// exists, but it does NOT add labels to a pre-existing volume —
	// and Compose never relabels one either (it only warns "volume
	// already exists but was not created by Docker Compose"). So we
	// pass the labels here on the create. The label set must match
	// ListVolumes' filter (com.docker.compose.project) or every later
	// snapshot of the restored instance finds an empty volume list.
	create := []string{"volume", "create"}
	if composeProject != "" {
		create = append(create,
			"--label", "com.docker.compose.project="+composeProject)
		if suffix := volumeSuffix(composeProject, volume); suffix != "" {
			create = append(create,
				"--label", "com.docker.compose.volume="+suffix)
		}
	}
	create = append(create, volume)
	if err := exec.CommandContext(ctx, "docker", create...).Run(); err != nil {
		return fmt.Errorf("create volume %q: %w", volume, err)
	}

	// Restore is a REPLACE, not a merge. `docker volume create` is a
	// no-op on an existing volume, and `tar xf` only adds/overwrites
	// the entries in the archive — files present in the volume but
	// absent from the snapshot survive. Restoring over a stopped-but-
	// extant instance would then leave old+new merged: e.g. Postgres
	// WAL segments written after the snapshot sitting alongside the
	// rewound data files, a state that is neither the snapshot nor the
	// pre-restore one and can fail recovery. Wipe the volume first so
	// the restored content equals the captured content exactly. The
	// wipe and the extract share one `docker run` so a single mount
	// (and a single container start) covers both.
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i",
		"-v", volume+":/dst",
		"alpine:3.20", "sh", "-c",
		"find /dst -mindepth 1 -delete && tar xf - -C /dst")
	cmd.Stdin = r
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// volumeSuffix recovers the Compose `volumes:` key for a volume named
// "<project>_<suffix>". Compose stamps that key as the
// com.docker.compose.volume label. Returns "" when the name doesn't
// carry the project prefix (a hand-crafted archive, or an older
// snapshot) — in which case we omit the per-volume label rather than
// guess. The project label alone is what ListVolumes filters on, so
// discovery still works; the volume label is best-effort fidelity.
func volumeSuffix(composeProject, volume string) string {
	prefix := composeProject + "_"
	if !strings.HasPrefix(volume, prefix) {
		return ""
	}
	return volume[len(prefix):]
}

// VolumeStorageAvailableBytes reports free space on Docker's volume
// store by mounting a throwaway anonymous volume into a probe
// container and reading `df` on the mount. The anonymous volume lives
// under Docker's data root, so the mount's filesystem IS the one
// restore extracts into — including the in-VM disk image on
// macOS/Windows that a host statfs cannot reach. `--rm -v /probe`
// (anonymous mount) is auto-removed with the container, so no volume
// leaks. POSIX `df -Pk` prints 1K-blocks in column 4 (Available) of
// the data row; we multiply back to bytes.
func (dockerVolumeArchiver) VolumeStorageAvailableBytes(ctx context.Context) (uint64, error) {
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", "/probe",
		"alpine:3.20", "df", "-Pk", "/probe").Output()
	if err != nil {
		return 0, fmt.Errorf("probe docker volume storage: %w", err)
	}
	return parseDfAvailableKB(string(out))
}

// parseDfAvailableKB extracts the Available column (1K-blocks) from
// POSIX `df -Pk` output and returns it as bytes. The -P (portable)
// format prints one data line with whitespace-separated fields:
// Filesystem 1024-blocks Used Available Capacity Mounted-on. We scan
// lines and return the first whose 4th field parses as a number — the
// header row's 4th field is "Available" (non-numeric) so it is skipped
// without a hard-coded line index.
func parseDfAvailableKB(dfOut string) (uint64, error) {
	for _, line := range strings.Split(strings.TrimSpace(dfOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		availKB, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			// Header row (or any non-data line) — skip.
			continue
		}
		return availKB * 1024, nil
	}
	return 0, fmt.Errorf("df output not parseable: %q", dfOut)
}
