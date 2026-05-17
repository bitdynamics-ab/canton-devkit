package localnet

import (
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildDoctor() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose host prerequisites for running Canton LocalNet",
		Long: `Runs every preflight check (Docker CLI, daemon, Compose v2, ports,
disk, memory, host prereqs) and prints a bug-report-friendly summary,
including system identifiers (OS/arch, Go runtime). Makes no changes
to the host.

Exit codes:
  0  All checks passed (warnings allowed)
  2  One or more checks failed`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return localnet.AsExitError(
				localnet.RunDoctor(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), &localnet.DoctorOptions{}))
		},
	}
	return cmd
}
