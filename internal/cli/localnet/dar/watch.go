package dar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	cdkdar "github.com/bitdynamics-ab/canton-devkit/internal/dar"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

// BIT-57: dar watch — rebuild on source change, re-upload on success.
//
// V1 uses a polling watcher (modtime sweep) rather than fsnotify so
// it works portably without an OS-specific dependency. For dev hot-
// deploy this is more than fast enough; if anyone needs sub-second
// reaction, swapping in fsnotify is an opt-in follow-up.
func buildWatch() *cobra.Command {
	var (
		conn     connectFlags
		project  string
		builder  string
		interval time.Duration
		vet      bool
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Rebuild and re-upload a Daml project on source change (hot-deploy loop)",
		Long: `Watch a Daml project's source tree and, on every change,
rebuild via dpm/daml and re-upload the resulting DAR to the
targeted participant.

Watch mechanism:
  Polls the source tree (every --interval, default 1s) for changes to
  any .daml file. Triggers a build-and-upload when a change is
  detected. Ctrl-C exits cleanly.

The build tool and upload path mirror ` + "`dar build-upload`" + `; see
that command's help for builder selection and connection flags.

Exit codes:
  0  Watch loop exited cleanly (Ctrl-C / context cancelled)
  1  Invalid arguments
  4  Initial build or upload failed`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			proj := project
			if proj == "" {
				wd, err := os.Getwd()
				if err != nil {
					return localnet.AsExitError(localnet.ExitUserError)
				}
				proj = wd
			}
			if _, err := os.Stat(filepath.Join(proj, "daml.yaml")); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"dar watch: no daml.yaml in %s — pass --project\n", proj)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			tool, err := pickBuilder(builder)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}

			client, err := conn.connect(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar watch: %s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer func() { _ = client.Close() }()

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Watching %s every %s — Ctrl-C to stop\n", proj, interval)

			// Initial build-and-upload pass. Failures here abort the
			// watch (likely a misconfigured project or unreachable
			// admin host — better to fail loud than silently sit in a
			// retry loop).
			lastHash, err := buildAndUpload(cmd.Context(), tool, proj, client, vet, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}

			tick := time.NewTicker(interval)
			defer tick.Stop()
			for {
				select {
				case <-cmd.Context().Done():
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "watch exiting.")
					return nil
				case <-tick.C:
					curHash, err := hashSources(proj)
					if err != nil {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "watch: hash sources: %s\n", err)
						continue
					}
					if curHash == lastHash {
						continue
					}
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "source changed; rebuilding...")
					if newHash, err := buildAndUpload(cmd.Context(), tool, proj, client, vet,
						cmd.OutOrStdout(), cmd.ErrOrStderr()); err == nil {
						lastHash = newHash
					}
					// On error, keep lastHash so the next tick re-tries.
					// The user sees the error on stderr.
				}
			}
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&project, "project", "", "Daml project root (dir containing daml.yaml). Defaults to cwd.")
	cmd.Flags().StringVar(&builder, "builder", "auto", "Build tool: auto, dpm, or daml.")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Polling interval for source changes.")
	cmd.Flags().BoolVar(&vet, "vet", true, "Vet packages on each upload.")
	return cmd
}

// buildAndUpload runs the build tool, locates the produced DAR, and
// uploads it via the open admin client. Returns the source-tree hash
// captured BEFORE the build so the watch loop knows the latest
// successfully-uploaded snapshot.
func buildAndUpload(
	ctx context.Context,
	tool, project string,
	client *admin.Client,
	vet bool,
	out io.Writer,
	errw io.Writer,
) (string, error) {
	hash, err := hashSources(project)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "watch: hash sources: %s\n", err)
		return "", err
	}

	darPath, err := runBuildCtx(ctx, tool, project, errw)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "build failed: %s\n", err)
		return "", err
	}
	// BIT-127 review fix: bounded read via cdkdar.ReadDARFile.
	data, err := cdkdar.ReadDARFile(darPath)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "read DAR %s: %s\n", darPath, err)
		return "", err
	}
	resp, err := client.Package.UploadDar(ctx, &adminproto.UploadDarRequest{
		Dars:           []*adminproto.UploadDarRequest_UploadDarData{{Bytes: data}},
		VetAllPackages: vet,
	})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "upload failed: %s\n", err)
		return "", err
	}
	for _, id := range resp.GetDarIds() {
		_, _ = fmt.Fprintf(out, "uploaded: %s\n", id)
	}
	return hash, nil
}

// runBuildCtx is the context-aware variant of runBuild (which lives in
// buildupload.go). Honours ctx for cancellation so Ctrl-C during a
// build doesn't leave a zombie compiler running.
func runBuildCtx(ctx context.Context, tool, project string, stderr io.Writer) (string, error) {
	c := exec.CommandContext(ctx, tool, "build")
	c.Dir = project
	c.Stderr = stderr
	if err := c.Run(); err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(project, ".daml", "dist", "*.dar"))
	if len(matches) == 0 {
		return "", fmt.Errorf("no DAR produced in .daml/dist/")
	}
	newest := matches[0]
	newestStat, _ := os.Stat(newest)
	for _, m := range matches[1:] {
		st, _ := os.Stat(m)
		if newestStat == nil || (st != nil && st.ModTime().After(newestStat.ModTime())) {
			newest = m
			newestStat = st
		}
	}
	return strings.TrimSpace(newest), nil
}

// hashSources walks the project tree and returns a hex SHA256 over
// (path, size, mtime) tuples for every *.daml file. Cheap change
// detector. Excludes generated dirs (.daml, .git) so a successful
// build doesn't immediately re-trigger.
func hashSources(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".daml" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".daml") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s|%d|%d\n", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
