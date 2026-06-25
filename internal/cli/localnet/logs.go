package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildLogs() *cobra.Command {
	opts := &localnet.LogsOptions{Tail: "100"}
	cmd := &cobra.Command{
		Use:           "logs [name]",
		Short:         "Tail or follow container logs",
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
			if err := localnet.ValidateName(opts.Name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunLogs(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Instance name. Can also be passed as a positional argument.")
	cmd.Flags().StringArrayVar(&opts.Services, "service", nil,
		"Compose service to filter on (may be repeated; omit for all).")
	cmd.Flags().BoolVarP(&opts.Follow, "follow", "f", false, "Stream logs (Ctrl-C to stop).")
	cmd.Flags().StringVar(&opts.Tail, "tail", "100",
		`Number of trailing lines per container (use "all" for everything).`)
	cmd.Flags().StringVar(&opts.Since, "since", "",
		"Show logs since duration (e.g. 10m, 1h) or RFC3339 timestamp.")
	return cmd
}
