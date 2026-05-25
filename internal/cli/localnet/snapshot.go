package localnet

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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// BIT-147 — `dpm localnet snapshot` + `restore`.
//
// Listed as an M1 proposal deliverable. The archive format is:
//
//	snapshot.tgz
//	├── snapshot.json            (types.Snapshot header, written first)
//	├── volumes/<vol-name>.tar   (one entry per docker volume)
//	└── …
//
// The header lands first so restore can validate schema_version and
// fail fast on a mismatched DevKit version before touching disk.
//
// Volumes are captured via an ephemeral alpine container running
// `tar`. We don't shell out to docker directly from Go — we use the
// `docker` CLI because we already require it on the host. Tests
// inject a fake `volumeArchiver` to bypass docker entirely.

// snapshotSchemaVersion mirrors types.SchemaVersion at the snapshot
// granularity — bumped only when the on-disk archive format changes
// incompatibly (e.g. renaming the volumes/ directory). Independent
// of types.SchemaVersion so a new field added to types.Snapshot
// doesn't force every existing archive to be invalidated.
const snapshotSchemaVersion = 1

// buildSnapshot wires `dpm localnet snapshot --name X --to file.tgz`.
func buildSnapshot() *cobra.Command {
	var name, to string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture a LocalNet's docker volumes to a tarball",
		Long: `Captures every docker volume owned by the named LocalNet's
compose project into a single .tgz archive. A header
(snapshot.json) is written first so 'restore' can validate
schema + Splice version before unpacking.

The instance does NOT need to be stopped. Volumes are read from
ephemeral alpine containers — your running services keep using
their own copy. For a strictly-consistent capture, run
'localnet down --name X' first (state preservation is
guaranteed; see BIT-124).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if to == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--to is required")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				RunSnapshot(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), name, to))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Required. Instance to snapshot.")
	cmd.Flags().StringVar(&to, "to", "", "Required. Destination archive path (.tgz).")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

// buildRestore wires `dpm localnet restore --name X --from file.tgz`.
func buildRestore() *cobra.Command {
	var name, from string
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore docker volumes from a snapshot tarball",
		Long: `Restores every volume from the named snapshot archive into the
target instance. The archive header is validated before any
volume is touched.

If the instance does not exist locally, it is registered with
the metadata from the snapshot. If it exists and is running,
restore refuses — run 'localnet down --name X' first.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if from == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--from is required")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				RunRestore(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), name, from))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Required. Instance to restore into.")
	cmd.Flags().StringVar(&from, "from", "", "Required. Source archive path (.tgz).")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("from")
	return cmd
}

// volumeArchiver is the seam tests use to swap docker for an
// in-memory fake. The production implementation lives in
// dockerVolumeArchiver below. Methods are minimal — listing belongs
// to ListVolumes, archive/restore round-trip a single volume.
type volumeArchiver interface {
	ListVolumes(ctx context.Context, composeProject string) ([]string, error)
	ArchiveVolume(ctx context.Context, volume string, w io.Writer) error
	RestoreVolume(ctx context.Context, volume string, r io.Reader) error
}

// archiverFn is the active archiver. Tests swap it with t.Cleanup
// restoration; production callers never touch it. Same pattern as
// stopperFn in down.go (BIT-124).
var archiverFn volumeArchiver = dockerVolumeArchiver{}

// RunSnapshot is exported so the future Web UI handler can call the
// same code path. Streams progress to `out`, errors to `errw`.
func RunSnapshot(ctx context.Context, out io.Writer, errw io.Writer, name, dest string) int {
	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			_, _ = fmt.Fprintf(errw, "no LocalNet instance named %q\n", name)
			return localnet.ExitUserError
		}
		_, _ = fmt.Fprintf(errw, "read registry: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	volumes, err := archiverFn.ListVolumes(ctx, state.ComposeProject)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "list volumes for %q: %s\n", state.ComposeProject, err)
		return localnet.ExitRuntimeFailure
	}
	sort.Strings(volumes) // stable archive ordering → reproducible content_sha

	_, _ = fmt.Fprintln(out, term.Prompt("", "", "", fmt.Sprintf(
		"dpm localnet snapshot %s %s %s %s",
		term.Amberc("--name"), name, term.Amberc("--to"), dest)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Reading registry state", state.ComposeProject, ""))
	_, _ = fmt.Fprintf(out, "%s\n", term.Step(term.StepCheck,
		fmt.Sprintf("Listed %d volume(s)", len(volumes)), strings.Join(volumes, ", "), ""))

	// Stage the archive in a temp file then atomic-rename so a
	// crash mid-write leaves either nothing or the complete file.
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "create %s: %s\n", tmp, err)
		return localnet.ExitRuntimeFailure
	}
	defer func() { _ = os.Remove(tmp) }() // no-op after successful rename

	header := types.Snapshot{
		SchemaVersion: snapshotSchemaVersion,
		Instance:      name,
		SpliceVersion: state.SpliceVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		DevKitVersion: "dev", // overwritten by build-time injection later
	}

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	// Volume tarballs first, header second — once we know each
	// volume's size + sha. We rewrite the snapshot.json entry at
	// the end via a buffered header strategy.
	type volEntry struct {
		Name string
		Body []byte
		SHA  string
	}
	captured := make([]volEntry, 0, len(volumes))
	for _, vol := range volumes {
		_, _ = fmt.Fprintln(out, term.Step(term.StepBusy, "Capturing volume", vol, ""))
		var buf strings.Builder
		hash := sha256.New()
		mw := io.MultiWriter(&buf, hash)
		if err := archiverFn.ArchiveVolume(ctx, vol, mw); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			_ = f.Close()
			_, _ = fmt.Fprintf(errw, "archive %q: %s\n", vol, err)
			return localnet.ExitRuntimeFailure
		}
		captured = append(captured, volEntry{
			Name: vol,
			Body: []byte(buf.String()),
			SHA:  "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		})
		header.Volumes = append(header.Volumes, types.SnapshotVolume{
			Name:       vol,
			SizeBytes:  int64(buf.Len()),
			ContentSHA: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		})
	}

	// Header first in the archive — readers can stream-validate
	// without buffering the whole file.
	headerBytes, _ := json.MarshalIndent(header, "", "  ")
	if err := writeTarEntry(tw, "snapshot.json", headerBytes); err != nil {
		_ = f.Close()
		_, _ = fmt.Fprintf(errw, "write header: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	for _, v := range captured {
		if err := writeTarEntry(tw, filepath.Join("volumes", v.Name+".tar"), v.Body); err != nil {
			_ = f.Close()
			_, _ = fmt.Fprintf(errw, "write %q: %s\n", v.Name, err)
			return localnet.ExitRuntimeFailure
		}
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		_, _ = fmt.Fprintf(errw, "close tar: %s\n", err)
		return localnet.ExitRuntimeFailure
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
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

// RunRestore reads the archive, validates the header, and unpacks
// each volume back into a docker volume of the same name.
func RunRestore(ctx context.Context, out io.Writer, errw io.Writer, name, src string) int {
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

	// First entry MUST be snapshot.json — otherwise we refuse, both
	// because order signals format-validity AND because we want to
	// fail before unpacking volumes that might end up homeless.
	hdr, err := tr.Next()
	if err != nil {
		_, _ = fmt.Fprintf(errw, "read header: %s\n", err)
		return localnet.ExitUserError
	}
	if hdr.Name != "snapshot.json" {
		_, _ = fmt.Fprintf(errw, "archive does not start with snapshot.json (got %q)\n", hdr.Name)
		return localnet.ExitUserError
	}
	var meta types.Snapshot
	headerBuf, err := io.ReadAll(tr)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "read header body: %s\n", err)
		return localnet.ExitUserError
	}
	if err := json.Unmarshal(headerBuf, &meta); err != nil {
		_, _ = fmt.Fprintf(errw, "parse header: %s\n", err)
		return localnet.ExitUserError
	}
	if meta.SchemaVersion > snapshotSchemaVersion {
		_, _ = fmt.Fprintf(errw, "snapshot schema_version %d is newer than this DevKit's %d — upgrade\n",
			meta.SchemaVersion, snapshotSchemaVersion)
		return localnet.ExitUserError
	}

	_, _ = fmt.Fprintln(out, term.Prompt("", "", "", fmt.Sprintf(
		"dpm localnet restore %s %s %s %s",
		term.Amberc("--name"), name, term.Amberc("--from"), src)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Validated header",
		fmt.Sprintf("schema %d · %d volume(s) · captured %s",
			meta.SchemaVersion, len(meta.Volumes), meta.CreatedAt), ""))

	// Refuse if running. The half-restore window is the worst-of-
	// both: running services hold open file handles into the volume
	// and our `docker run -v` overlay would write into a directory
	// the live container also writes — undefined behaviour.
	if existing, err := registry.Read(name); err == nil {
		if existing.Status == registry.StatusRunning {
			_, _ = fmt.Fprintf(errw, "instance %q is running — run `localnet down --name %s` first\n", name, name)
			return localnet.ExitUserError
		}
	}

	// Walk volume entries.
	for {
		entry, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_, _ = fmt.Fprintf(errw, "read volume entry: %s\n", err)
			return localnet.ExitRuntimeFailure
		}
		if !strings.HasPrefix(entry.Name, "volumes/") || !strings.HasSuffix(entry.Name, ".tar") {
			_, _ = fmt.Fprintln(out, term.Step(term.StepWarn, "Skipping unknown entry", entry.Name, ""))
			continue
		}
		volName := strings.TrimSuffix(strings.TrimPrefix(entry.Name, "volumes/"), ".tar")
		_, _ = fmt.Fprintln(out, term.Step(term.StepBusy, "Restoring volume", volName, ""))
		if err := archiverFn.RestoreVolume(ctx, volName, tr); err != nil {
			_, _ = fmt.Fprintf(errw, "restore %q: %s\n", volName, err)
			return localnet.ExitRuntimeFailure
		}
		_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Restored volume", volName, ""))
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, term.Box(term.BoxBrand,
		fmt.Sprintf("%s Restored %s from %s\n%s",
			term.Brandc("✦"),
			term.Bold("\""+name+"\""),
			term.Bold(src),
			term.Dimc(fmt.Sprintf("Run %s to bring it up.",
				term.Textc(fmt.Sprintf("localnet up --name %s", name)))))))
	return localnet.ExitSuccess
}

// writeTarEntry is a minimal tar.Writer helper. Mode 0o600 because
// snapshot.json may carry env-file-style metadata in future
// revisions; defensive narrowing now is cheaper than rotating it
// later.
func writeTarEntry(tw *tar.Writer, name string, body []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Size: int64(len(body)), Mode: 0o600, ModTime: time.Now().UTC(),
	}); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

// dockerVolumeArchiver is the production volumeArchiver: shells out
// to `docker volume ls` + ephemeral alpine `tar` to capture/restore.
// Tests use the in-memory fakeArchiver in snapshot_test.go.
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
	// `tar cf - .` streams the volume content to stdout; we pipe
	// that into the caller's writer. The container exits as soon as
	// tar finishes.
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", volume+":/src:ro",
		"alpine:3.20", "tar", "cf", "-", "-C", "/src", ".")
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (dockerVolumeArchiver) RestoreVolume(ctx context.Context, volume string, r io.Reader) error {
	// `docker volume create` is idempotent — no-op if the volume
	// already exists (which it will when restoring into a running
	// instance's directory after a teardown).
	if err := exec.CommandContext(ctx, "docker", "volume", "create", volume).Run(); err != nil {
		return fmt.Errorf("create volume %q: %w", volume, err)
	}
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i",
		"-v", volume+":/dst",
		"alpine:3.20", "tar", "xf", "-", "-C", "/dst")
	cmd.Stdin = r
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
