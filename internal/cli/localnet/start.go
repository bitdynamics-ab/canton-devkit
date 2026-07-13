package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildStart() *cobra.Command {
	opts := &localnet.StartOptions{}
	cmd := &cobra.Command{
		Use:   "start [name]",
		Short: "Start a LocalNet instance, creating it if it doesn't exist yet",
		Long: `Bring a LocalNet instance to a running state based on its
current status:

  - stopped with runtime state present: start existing services
  - stopped with runtime state removed: recreate services
  - not registered yet: create and start the LocalNet
  - already running: no action
  - paused: use 'localnet resume' instead

Use 'localnet up' directly when you need to choose a version or profiles.`,
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
			// TextProgress renders RunUp's typed step events on the
			// up-fallback paths; the fast compose-start path writes
			// plain lines to stdout.
			prog := localnet.NewTextProgress(cmd.OutOrStdout(), cmd.ErrOrStderr())
			return localnet.AsExitError(
				localnet.RunStart(cmd.Context(), prog, cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the instance to start. Can also be passed as a positional argument.")
	cmd.Flags().BoolVar(&opts.SkipWait, "no-wait", false, "Skip the post-start readiness wait when starting existing services.")
	return cmd
}
