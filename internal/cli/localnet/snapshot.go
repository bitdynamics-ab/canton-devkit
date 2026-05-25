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
	"path"
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
// First-cut review surfaced five blockers; this file addresses
// each:
//
//   1. Zip Slip on restore — `volumes/foo.tar` was trusted verbatim.
//      Fix: validateArchivePath rejects anything that isn't strictly
//      `volumes/<safename>.tar` and `validateVolumeName` constrains
//      the inner component.
//   2. Whole archive buffered in RAM. Fix: streamed straight into
//      tar.Writer; restore reads streaming via io.Pipe.
//   3. Didn't capture registry.State — restored snapshot was
//      unbringable. Fix: state.json is the SECOND archive entry;
//      restore re-registers via registry.Write.
//   4. SHA "verification" was decorative. Fix: streaming sha256 on
//      both writer + reader; mismatch aborts with the volume name.
//   5. Splice-version mismatch silently corrupted. Fix:
//      header.SpliceVersion compared to existing instance (or
//      embedded state.json) and refused unless --force.
//
// On-disk layout (strict):
//
//   snapshot.tgz
//   ├── snapshot.json           (types.Snapshot header, FIRST)
//   ├── state.json              (registry.State, SECOND)
//   ├── volumes/<vol>.tar       (one per docker volume; <vol> must
//   │                            validate via validateVolumeName)
//   └── …

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

func buildSnapshot() *cobra.Command {
	var name, to string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture a LocalNet's docker volumes + registry state to a tarball",
		Long: `Captures every docker volume owned by the named LocalNet's
compose project, plus the instance's registry.State, into a single
.tgz archive. The header (snapshot.json) is written FIRST so
'restore' can stream-validate schema + Splice version before
unpacking anything.

The instance does NOT need to be stopped. Volumes are read from
ephemeral alpine containers — services keep using their own copy.
For a strictly-consistent capture, run 'localnet down --name X'
first (state preservation guaranteed by BIT-124).`,
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

func buildRestore() *cobra.Command {
	var name, from string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore docker volumes + registry state from a snapshot tarball",
		Long: `Restores every volume from the named snapshot archive AND
re-registers the instance via the state.json embedded in the
archive. Header is validated before any volume is touched.

If the snapshot's Splice version differs from an existing local
instance's, restore refuses unless --force is set — restoring
volumes formatted for a different binary is unsafe by default.

If the instance does not exist locally, it is registered from the
embedded state.json with Status=stopped. If it exists and is
running, restore refuses — run 'localnet down --name X' first.`,
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
				RunRestore(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
					name, from, force))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Required. Instance to restore into.")
	cmd.Flags().StringVar(&from, "from", "", "Required. Source archive path (.tgz).")
	cmd.Flags().BoolVar(&force, "force", false,
		"Override Splice-version mismatch refusal. Use with care.")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("from")
	return cmd
}

// volumeArchiver is the seam tests use; production uses
// dockerVolumeArchiver. All three methods are streaming so neither
// side ever holds a whole volume in memory.
type volumeArchiver interface {
	ListVolumes(ctx context.Context, composeProject string) ([]string, error)
	ArchiveVolume(ctx context.Context, volume string, w io.Writer) error
	RestoreVolume(ctx context.Context, volume string, r io.Reader) error
}

var archiverFn volumeArchiver = dockerVolumeArchiver{}

// RunSnapshot streams a snapshot to dest. Memory footprint per
// volume is bounded by the tar copy buffer, not the volume size.
// Two-pass strategy: write each volume body to an intermediate tar
// file (so we know its size + SHA when writing the header), then
// copy header + state.json + intermediate into the real archive in
// strict on-disk order.
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
	sort.Strings(volumes)
	for _, v := range volumes {
		if err := validateVolumeName(v); err != nil {
			_, _ = fmt.Fprintf(errw, "volume %q rejected: %s\n", v, err)
			return localnet.ExitRuntimeFailure
		}
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

	existing, _ := registry.Read(name)
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

	// Re-register from embedded state, keyed by the user-supplied
	// --name so "restore as a different name" works without extra
	// flags.
	toWrite := *embedded
	toWrite.Name = name
	toWrite.Status = registry.StatusStopped
	if err := registry.Write(&toWrite); err != nil {
		_, _ = fmt.Fprintf(errw, "register restored instance: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

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
		volName, ok := validateArchivePath(hdr.Name)
		if !ok {
			_, _ = fmt.Fprintln(out, term.Step(term.StepWarn,
				"Skipping unknown entry", hdr.Name, ""))
			if _, err := io.CopyN(io.Discard, tr, hdr.Size); err != nil && err != io.EOF {
				_, _ = fmt.Fprintf(errw, "discard %q: %s\n", hdr.Name, err)
				return localnet.ExitRuntimeFailure
			}
			continue
		}
		if hdr.Size > maxArchiveEntry {
			_, _ = fmt.Fprintf(errw, "volume %q size %d exceeds %d-byte ceiling\n",
				volName, hdr.Size, maxArchiveEntry)
			return localnet.ExitRuntimeFailure
		}
		_, _ = fmt.Fprintln(out, term.Step(term.StepBusy, "Restoring volume", volName, ""))

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
		restErr := archiverFn.RestoreVolume(ctx, volName, pr)
		streamErr := <-copyErr
		if restErr != nil {
			_, _ = fmt.Fprintf(errw, "restore %q: %s\n", volName, restErr)
			return localnet.ExitRuntimeFailure
		}
		if streamErr != nil && streamErr != io.EOF {
			_, _ = fmt.Fprintf(errw, "stream %q: %s\n", volName, streamErr)
			return localnet.ExitRuntimeFailure
		}
		got := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
		want, hadExpected := expectedSHA[volName]
		if hadExpected && got != want {
			_, _ = fmt.Fprintf(errw,
				"volume %q SHA mismatch: archive reports %s, computed %s\n",
				volName, want, got)
			return localnet.ExitRuntimeFailure
		}
		restored++
		_, _ = fmt.Fprintln(out, term.Step(term.StepCheck, "Restored volume", volName, ""))
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

// validateArchivePath is the Zip Slip gate — the reviewer-flagged
// blocker. Rejects anything that isn't strictly
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

func (dockerVolumeArchiver) RestoreVolume(ctx context.Context, volume string, r io.Reader) error {
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
