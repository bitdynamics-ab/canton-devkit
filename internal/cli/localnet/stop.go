package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildStop() *cobra.Command {
	opts := &localnet.StopOptions{}
	cmd := &cobra.Command{
		Use:   "stop [name]",
		Short: "Gracefully stop a running LocalNet instance (docker compose stop)",
		Long: fmt.Sprintf(`Gracefully stop a running instance with 'docker compose stop'
(SIGTERM → SIGKILL): the container processes exit and free their CPU
and memory, but the containers, networks, and volumes stay in place.
Restart cheaply with '%[1]s start' — no image pull or container
re-create. Ledger state is preserved.

For a full teardown that removes containers and networks, use
'%[1]s down'. To only freeze processes without releasing memory
(and resume instantly), use '%[1]s pause'.`, cliprefix.Get()),
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
				localnet.RunStop(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the instance to stop. Can also be passed as a positional argument.")
	return cmd
}
