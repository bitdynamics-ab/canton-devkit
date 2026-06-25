package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildPause() *cobra.Command {
	opts := &localnet.PauseOptions{}
	cmd := &cobra.Command{
		Use:   "pause [name]",
		Short: "Freeze a running LocalNet instance (docker compose pause)",
		Long: `Freeze a running instance with 'docker compose pause' (SIGSTOP):
containers hold their in-memory state and published ports but stop using
CPU. Resume with 'localnet resume' — no boot cost, no port changes. The
"stepping away, free my CPU but keep my ledger" control.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveName(cmd, args)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			opts.Name = name
			return localnet.AsExitError(
				localnet.RunPause(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the instance to pause. Can also be passed as a positional argument.")
	return cmd
}

func buildResume() *cobra.Command {
	opts := &localnet.PauseOptions{}
	cmd := &cobra.Command{
		Use:   "resume [name]",
		Short: "Resume a paused LocalNet instance (docker compose unpause)",
		Long: `Resume a paused instance with 'docker compose unpause' (SIGCONT).
The inverse of 'localnet pause' — containers continue exactly where they
were frozen, with no readiness wait and unchanged ports.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveName(cmd, args)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			opts.Name = name
			return localnet.AsExitError(
				localnet.RunResume(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the instance to resume. Can also be passed as a positional argument.")
	return cmd
}
