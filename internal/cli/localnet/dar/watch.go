package dar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// dar watch — rebuild on source change, re-upload on success.
//
// V1 uses a polling watcher (modtime sweep) rather than fsnotify so
// it works portably without an OS-specific dependency. For dev hot-
// deploy this is more than fast enough; if anyone needs sub-second
// reaction, swapping in fsnotify is an opt-in follow-up.
func buildWatch() *cobra.Command {
	var (
		conn      connectFlags
		project   string
		builder   string
		interval  time.Duration
		vet       bool
		publishTo string
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Rebuild and re-upload a Daml project on source change (hot-deploy loop)",
		Long: `Watch a Daml project's source tree and, on every change,
rebuild via dpm/daml and re-upload the resulting DAR to the
targeted participant.

Watch mechanism:
  Polls the source tree (every --interval, default 1s) for changes to
  any .daml or .yaml file (so daml.yaml version bumps are caught too).
  Triggers a build-and-upload when a change is detected. Ctrl-C exits
  cleanly.

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

			pub := newWatchPublisher(publishTo, conn.Instance)
			pub.emit(cmd.Context(), "watch_started", "", proj, cmd.ErrOrStderr())
			defer pub.emit(context.Background(), "watch_stopped", "", "", cmd.ErrOrStderr())

			// Initial build-and-upload pass. Failures here abort the
			// watch (likely a misconfigured project or unreachable
			// admin host — better to fail loud than silently sit in a
			// retry loop).
			lastHash, err := buildAndUploadWithPublish(cmd.Context(), tool, proj, client, vet, pub, cmd.OutOrStdout(), cmd.ErrOrStderr())
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
					if newHash, err := buildAndUploadWithPublish(cmd.Context(), tool, proj, client, vet, pub,
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
	cmd.Flags().StringVar(&publishTo, "publish-to", "", "Base URL of a running `dpm localnet ui` to publish hot-deploy events to (e.g. http://127.0.0.1:7777). Best-effort; failures are logged to stderr and do not abort the watch loop. Uses --instance to tag events.")
	return cmd
}

// watchPublisher is the fire-and-forget client that POSTs hot-
// deploy events to a running UI server. Used by the watch loop to
// surface the "watching" badge + last-rebuild timestamp in the
// DAR screen.
//
// Failures (UI not running, network blip, slow response) are
// logged to stderr but never abort the watch loop — the
// build/upload behaviour is the source of truth; the SSE bridge
// is decoration.
type watchPublisher struct {
	baseURL  string
	instance string
	client   *http.Client
}

func newWatchPublisher(baseURL, instance string) *watchPublisher {
	if baseURL == "" || instance == "" {
		return nil
	}
	return &watchPublisher{
		baseURL:  strings.TrimRight(baseURL, "/"),
		instance: instance,
		client:   &http.Client{Timeout: 2 * time.Second},
	}
}

// emit pushes one event to the UI server. event is one of
// "rebuild_started", "rebuild_finished", "upload_finished",
// "watch_started", "watch_stopped". darID may be empty when no
// DAR is in flight yet.
func (p *watchPublisher) emit(ctx context.Context, event, darID, detail string, errw io.Writer) {
	if p == nil {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"instance": p.instance,
		"dar_id":   darID,
		"event":    event,
		"at":       time.Now().Unix(),
		"detail":   detail,
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		p.baseURL+"/api/dar/watch/publish", bytes.NewReader(body))
	if err != nil {
		_, _ = fmt.Fprintf(errw, "watch publish: build request: %s\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// withOriginCheck on the UI server allows requests with no
	// Origin (curl-style); we are exactly that case.
	resp, err := p.client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "watch publish: %s (UI may not be running; events disabled)\n", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		_, _ = fmt.Fprintf(errw, "watch publish: UI returned %d\n", resp.StatusCode)
	}
}

// buildAndUploadWithPublish is the build → upload pipeline plus
// lifecycle SSE publishes for the Web UI hot-deploy indicator.
// When pub is nil the publishes are no-ops, so this is also the
// no-UI code path.
//
// Sequence:
//
//	rebuild_started (no dar_id)
//	  → build
//	rebuild_finished (dar_id from the produced file basename)
//	  → upload
//	upload_finished (dar_id = first uploaded id from the response)
//
// Detail strings carry the dar filename / error message so the UI
// can render meaningful tooltips.
func buildAndUploadWithPublish(
	ctx context.Context,
	tool, project string,
	client *admin.Client,
	vet bool,
	pub *watchPublisher,
	out io.Writer,
	errw io.Writer,
) (string, error) {
	pub.emit(ctx, "rebuild_started", "", "", errw)

	hash, err := hashSources(project)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "watch: hash sources: %s\n", err)
		return "", err
	}

	darPath, err := runBuildCtx(ctx, tool, project, errw)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "build failed: %s\n", err)
		pub.emit(ctx, "rebuild_finished", "", "build failed: "+err.Error(), errw)
		return "", err
	}
	pub.emit(ctx, "rebuild_finished", "", filepath.Base(darPath), errw)

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
		pub.emit(ctx, "upload_finished", "", "upload failed: "+err.Error(), errw)
		return "", err
	}
	for _, id := range resp.GetDarIds() {
		_, _ = fmt.Fprintf(out, "uploaded: %s\n", id)
	}
	firstID := ""
	if ids := resp.GetDarIds(); len(ids) > 0 {
		firstID = ids[0]
	}
	pub.emit(ctx, "upload_finished", firstID, filepath.Base(darPath), errw)
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
// (path, size, mtime) tuples for every build-relevant source file.
// Cheap change detector. Excludes generated dirs (.daml, .git) so a
// successful build doesn't immediately re-trigger.
//
// Build-relevant means *.daml AND *.yaml: daml.yaml is the file you
// edit to bump the package `version` (the canonical SCU loop) or to
// change name/dependencies/build-options, and multi-package projects
// keep additional daml/*.yaml manifests. Hashing only *.daml would
// silently ignore the most upgrade-relevant edits, leaving the user in
// a hot-deploy loop that never rebuilds on a version bump.
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
		if !isWatchedSource(d.Name()) {
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

// isWatchedSource reports whether a filename is a build input the
// watch loop should hash. Daml source plus YAML build manifests
// (daml.yaml and any *.yaml the project layout uses); the generated
// .daml/ tree is already pruned in hashSources so a .yaml emitted
// there can't re-trigger.
func isWatchedSource(name string) bool {
	return strings.HasSuffix(name, ".daml") ||
		strings.HasSuffix(name, ".yaml") ||
		strings.HasSuffix(name, ".yml")
}
