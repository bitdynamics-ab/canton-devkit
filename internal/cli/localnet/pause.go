package localnet

import (
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildPause() *cobra.Command {
	opts := &localnet.PauseOptions{}
	cmd := &cobra.Command{
		Use:   "pause",
		Short: "Freeze a running LocalNet instance (docker compose pause)",
		Long: `Freeze a running instance with 'docker compose pause' (SIGSTOP):
containers hold their in-memory state and published ports but stop using
CPU. Resume with 'localnet resume' — no boot cost, no port changes. The
"stepping away, free my CPU but keep my ledger" control.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return localnet.AsExitError(
				localnet.RunPause(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Required. Name of the instance to pause.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func buildResume() *cobra.Command {
	opts := &localnet.PauseOptions{}
	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused LocalNet instance (docker compose unpause)",
		Long: `Resume a paused instance with 'docker compose unpause' (SIGCONT).
The inverse of 'localnet pause' — containers continue exactly where they
were frozen, with no readiness wait and unchanged ports.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return localnet.AsExitError(
				localnet.RunResume(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Required. Name of the instance to resume.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
