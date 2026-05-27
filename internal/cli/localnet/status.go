package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildStatus() *cobra.Command {
	opts := &localnet.StatusOptions{}
	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show per-service health for a Canton LocalNet instance",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(opts.Name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			if err := localnet.ValidateFormat(opts.Format, "text", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunStatus(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Required. Instance name.")
	cmd.Flags().StringVar(&opts.Format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
