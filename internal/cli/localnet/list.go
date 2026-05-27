package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildList() *cobra.Command {
	opts := &localnet.ListOptions{Format: "text"}
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List every Canton LocalNet instance known to this DevKit",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(opts.Format, "text", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunList(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().BoolVar(&opts.All, "all", false, "Include stopped/failed instances (default: running only).")
	cmd.Flags().StringVar(&opts.Format, "format", "text", "Output format: text or json.")
	return cmd
}
