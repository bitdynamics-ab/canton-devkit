package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildDown() *cobra.Command {
	opts := &localnet.DownOptions{}
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop a Canton LocalNet instance and remove its containers, network, and volumes",
		Long: `Stop a Canton LocalNet instance. Idempotent — running against an
already-stopped instance exits 0. Reads compose project + files from
the per-instance state.json so no --env-file is needed.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(opts.Name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunDown(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Required. Name of the instance to stop.")
	cmd.Flags().BoolVar(&opts.KeepData, "keep-data", false,
		"Keep ~/.canton/localnet/<name>/ (overlay env, state.json).")
	cmd.Flags().BoolVar(&opts.KeepCache, "keep-cache", false,
		"Reserved for future use (the splice cache is shared across instances).")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
