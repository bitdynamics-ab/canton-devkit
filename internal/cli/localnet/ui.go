package localnet

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui"
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
		port int
		host string
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
			assets, err := ui.AssetsHandler()
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "load embedded assets: %s\n", err)
				return err
			}
			srv := ui.New(ui.Config{
				Host:   host,
				Port:   port,
				Router: ui.NewRouter(assets),
			})

			// Bind FIRST so we can print the actual URL with the
			// real port (matters when --port=0).
			addr, err := srv.Listen()
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", err)
				return err
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
					return err
				}
				// Wait for Serve to return after Shutdown.
				<-errCh
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc("stopped."))
				return nil
			case err := <-errCh:
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"server error: %s\n", err)
				}
				return err
			}
		},
	}
	cmd.Flags().IntVar(&port, "port", 7777,
		"TCP port to bind. 0 = OS-assigned (prints the actual port).")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1",
		"Loopback interface to bind. Non-loopback values are accepted "+
			"but strongly discouraged — the UI exposes credentials.")
	return cmd
}
