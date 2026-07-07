package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildClean() *cobra.Command {
	opts := &localnet.CleanOptions{}
	cmd := &cobra.Command{
		Use:     "remove",
		Aliases: []string{"clean"},
		Short:   "Remove Canton LocalNet state, containers, and volumes for an instance",
		Long: `Remove DevKit-managed state for a named instance (or all instances
with --all): the registry entry, the per-instance data directory, and
any lingering docker containers/volumes for the compose project.

remove (alias: clean) is the housekeeping / recovery verb — use it to
reclaim disk from stopped instances or to scrub orphaned/corrupted
state. A RUNNING instance is refused unless --force (which tears it
down first). Use --dry-run to preview what would be removed.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !opts.All && opts.Name == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "provide --name <instance> or --all")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if opts.All && opts.Name != "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--all and --name are mutually exclusive")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunClean(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the instance to remove.")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Remove every registered instance.")
	cmd.Flags().BoolVar(&opts.Force, "force", false,
		"Tear down and remove even a running instance (runs `down` first).")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false,
		"Print what would be removed without removing anything.")
	return cmd
}
