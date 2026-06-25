package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildRestart() *cobra.Command {
	opts := &localnet.RestartOptions{}
	cmd := &cobra.Command{
		Use:   "restart [name]",
		Short: "Restart a LocalNet instance or specific services",
		Long: `Bounce a running instance via 'docker compose restart' without
removing containers, networks, or volumes. Re-runs the readiness wait
and re-captures Canton's gRPC ports (which Docker re-assigns on
restart). Use --service to restart only specific compose services.`,
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
			// Format-validate each --service up front (cheap, no
			// docker) — same regex container/logs use. RunRestart
			// additionally checks membership against the live project.
			for _, svc := range opts.Services {
				if !validServiceArg.MatchString(svc) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"invalid --service %q: must be a compose service name (alphanumeric, dot, dash, underscore)\n", svc)
					return localnet.AsExitError(localnet.ExitUserError)
				}
			}
			return localnet.AsExitError(
				localnet.RunRestart(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the instance to restart. Can also be passed as a positional argument.")
	cmd.Flags().StringSliceVar(&opts.Services, "service", nil,
		"Restart only these compose services (e.g. canton, splice). Repeat or comma-separate. Omit to restart all.")
	cmd.Flags().BoolVar(&opts.SkipWait, "no-wait", false,
		"Skip the post-restart readiness wait.")
	return cmd
}
