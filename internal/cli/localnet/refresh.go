package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/handlers"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildRefresh wires `dpm localnet refresh` — the CLI mirror of
// the background docker→registry reconciler that runs inside
// `dpm localnet ui`. Per AGENTS.md "CLI ↔ Web UI parity": users
// who never start the Web UI shouldn't be stuck with a stale
// `failed`/`partial` status forever after fixing the underlying
// cause (Docker memory bump, manual container restart, etc.).
//
// Behaviour mirrors handlers.StartReconciler's per-instance pass:
// for each registered instance (or just `--name <inst>` if given),
// probe docker via `compose ps`, evaluate the aggregate status,
// and write back to the registry if it changed.
//
// Logs each flip to stdout so the user sees the catch-up; silent
// when nothing changed (no `everything was already correct, OK`
// noise on healthy systems).
func buildRefresh() *cobra.Command {
	var (
		name   string
		format string
	)
	cmd := &cobra.Command{
		Use:           "refresh",
		Short:         "Sync registry status with docker",
		Long:          "Run docker compose ps for one or all registered instances and update registry status when it differs from docker. Useful after a timed-out bring-up that later became healthy.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet_validateFormat(format, "text", "json"); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			var names []string
			if name != "" {
				if err := registry.ValidateName(name); err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"invalid instance name: "+err.Error())
					return localnet.AsExitError(localnet.ExitUserError)
				}
				if _, err := registry.Read(name); err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"instance not registered: "+name)
					return localnet.AsExitError(localnet.ExitUserError)
				}
				names = []string{name}
			} else {
				idx, err := registry.ReadIndex()
				if err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"read registry index: "+err.Error())
					return localnet.AsExitError(localnet.ExitRuntimeFailure)
				}
				for _, e := range idx.Entries {
					names = append(names, e.Name)
				}
			}

			if len(names) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc(
					"no registered instances — nothing to refresh"))
				return nil
			}

			type flip struct {
				Instance string `json:"instance"`
				Old      string `json:"old"`
				New      string `json:"new"`
				Changed  bool   `json:"changed"`
			}
			results := make([]flip, 0, len(names))
			for _, n := range names {
				oneCtx, cancelOne := context.WithTimeout(ctx, 10*time.Second)
				old, neu, flipped := handlers.ReconcileOne(oneCtx, n)
				cancelOne()
				results = append(results, flip{
					Instance: n,
					Old:      string(old),
					New:      string(neu),
					Changed:  flipped,
				})
			}

			if format == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"reconciled": results,
				})
			}

			changed := 0
			for _, r := range results {
				if r.Changed {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Step(term.StepCheck,
						fmt.Sprintf("%s: %s → %s", r.Instance, r.Old, r.New), "", ""))
					changed++
				}
			}
			if changed == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc(
					fmt.Sprintf("all %d instance(s) already in sync with docker", len(names))))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "",
		"Restrict to one instance (default: refresh all)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}
