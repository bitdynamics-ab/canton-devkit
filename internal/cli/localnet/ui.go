package localnet

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildUI wires `dpm localnet ui` — BIT-129, the M2 Web UI entry point.
//
// This first slice (P2-01 skeleton) brings up the HTTP server only:
// /healthz, /api/version handshake, and the embedded Vite bundle (which
// currently renders the BIT-129 placeholder until BIT-133 lifts the
// React frontend in). Follow-on PRs slot in:
//
//   - BIT-130 (P2-02): SSE /events stream
//   - BIT-131 (P2-03): REST /api/* handlers
//   - BIT-133 (P2-05): real Vite/React bundle replacing the placeholder
//
// # Why ship the skeleton first
//
// The reviewer surface that matters most for M2 isn't any single
// handler — it's the lifecycle + bind + asset-embed shape. Landing
// that as its own PR means BIT-130 and BIT-131 reviews focus on
// their own surfaces instead of re-litigating the loopback binding
// or the SPA fallback every round.
//
// # `--port 0`
//
// Bind 0 = OS-assigned. Two callers care:
//   - tests (TestUI_BindAndShutdownReadiness drives it),
//   - CI scripts that want to spin up the UI on a random port to
//     run a smoke test without colliding with a running dev server.
//
// We always print the ACTUAL bound port (resolved from net.Listen) so
// "Open http://127.0.0.1:7777" never misleads the user when 7777 is
// already in use and we fell back to a free port via `--port 0`.
func buildUI() *cobra.Command {
	var (
		port             int
		host             string
		allowNonLoopback bool
	)
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Launch the Web UI (loopback only)",
		Long: `Starts the canton-devkit Web UI server, bound strictly to a
loopback interface (127.0.0.1 by default). Serves the embedded
Vite/React bundle plus REST endpoints under /api/ and the SSE
/events stream.

For security, the bind interface is locked to loopback in CLI flags;
remote access requires an SSH tunnel. The UI handles JWTs and party
identifiers and is not designed for LAN-wide exposure.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Reviewer pin (PR #41 #4): every error path from this
			// command must wrap with localnet.AsExitError so the
			// outer cobra/app exit-code plumbing surfaces the right
			// numeric code (matches CLAUDE.md's "ExitCodeError must
			// not silently collapse through wrappers" rule).
			assets, err := ui.AssetsHandler()
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "load embedded assets: %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			// Placeholder-bundle guard (PR #41 #5). If the binary
			// was built without `make frontend`, the embedded dist
			// is the placeholder shipped at clone time. Print one
			// stderr warning so an operator running a release-mode
			// binary knows the frontend will be a dev placeholder,
			// not the real Vite build.
			if ui.IsPlaceholderBundle() {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), term.Warnc(
					"warning: serving the build-time placeholder frontend — run `make frontend` before `go build` to embed the real Vite bundle"))
			}
			hub := stream.New()
			srv := ui.New(ui.Config{
				Host:             host,
				Port:             port,
				Router:           ui.NewRouter(assets, hub),
				AllowNonLoopback: allowNonLoopback,
			})

			// Bind FIRST so we can print the actual URL with the
			// real port (matters when --port=0).
			addr, err := srv.Listen()
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", err)
				// Listen failures: bind-refused (--host non-loopback
				// without flag) is ExitUserError; everything else
				// (port in use, EACCES) is ExitRuntimeFailure.
				if errIsNonLoopback(err) {
					return localnet.AsExitError(localnet.ExitUserError)
				}
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			url := fmt.Sprintf("http://%s/", addr)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Step(term.StepCheck,
				"Web UI listening", url, ""))
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc(
				"Press Ctrl+C to stop. Loopback-only — for remote access use `ssh -L`."))

			// Wire SIGINT/SIGTERM to a graceful shutdown.
			ctx, stop := signal.NotifyContext(cmd.Context(),
				syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve() }()

			select {
			case <-ctx.Done():
				// Give in-flight requests 5s to drain. 5s is the
				// same number `dpm localnet down` uses for its
				// compose-down deadline — keep parity so users
				// remember one number for "shutdown patience".
				shutdownCtx, cancel := context.WithTimeout(
					context.Background(), 5*time.Second)
				defer cancel()
				if err := srv.Shutdown(shutdownCtx); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"graceful shutdown: %s\n", err)
					// Deadline exceeded mid-drain → ExitTimeout
					// (matches the down command's convention).
					if shutdownCtx.Err() != nil {
						return localnet.AsExitError(localnet.ExitTimeout)
					}
					return localnet.AsExitError(localnet.ExitRuntimeFailure)
				}
				// PR #42 round-2 #3: explicitly close the SSE hub
				// so any lingering subscriber goroutines unblock
				// on their channel close. http.Server.Shutdown
				// doesn't drain SSE streams — the loop sits in
				// `case ev, ok := <-eventCh`, which only returns
				// when the channel closes (cancel) OR ctx.Done
				// fires on the request. Without Hub.Close the
				// goroutine can outlive the server briefly.
				hub.Close()

				// Wait for Serve to return after Shutdown.
				<-errCh
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc("stopped."))
				return nil
			case err := <-errCh:
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"server error: %s\n", err)
					return localnet.AsExitError(localnet.ExitRuntimeFailure)
				}
				return nil
			}
		},
	}
	cmd.Flags().IntVar(&port, "port", 7777,
		"TCP port to bind. 0 = OS-assigned (prints the actual port).")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1",
		"Loopback interface to bind. Non-loopback values are REFUSED "+
			"unless --allow-non-loopback is also set.")
	cmd.Flags().BoolVar(&allowNonLoopback, "allow-non-loopback", false,
		"Allow binding on a non-loopback interface. Exposes the UI "+
			"(JWTs, party IDs) on the network. Use only with an SSH "+
			"tunnel or a firewall in front. Default: refused.")
	return cmd
}

// errIsNonLoopback unwraps to ui.ErrNonLoopbackBind so the CLI
// can choose ExitUserError (user picked a bad --host) over
// ExitRuntimeFailure (port-in-use / EACCES / etc.).
func errIsNonLoopback(err error) bool {
	if err == nil {
		return false
	}
	for e := err; e != nil; {
		if e == ui.ErrNonLoopbackBind {
			return true
		}
		type unwrap interface{ Unwrap() error }
		u, ok := e.(unwrap)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
