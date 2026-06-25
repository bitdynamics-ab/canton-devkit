package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildClean() *cobra.Command {
	opts := &localnet.CleanOptions{}
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove instance state, containers, and volumes",
		Long: `Remove the registry entry, per-instance data directory, and docker
containers/volumes for a named instance (or all instances with --all).

Refuses a running instance unless --force (runs down first).
Use --dry-run to preview what would be removed.`,
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
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the instance to clean.")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Clean every registered instance.")
	cmd.Flags().BoolVar(&opts.Force, "force", false,
		"Tear down and clean even a running instance (runs `down` first).")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false,
		"Print what would be removed without removing anything.")
	return cmd
}
