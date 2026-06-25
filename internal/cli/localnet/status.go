package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildStatus() *cobra.Command {
	opts := &localnet.StatusOptions{}
	cmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Show LocalNet services, endpoints, identities",
		Long: `Read ~/.canton-devkit/localnet/<name>/state.json and docker compose
ps output for live service health. The registry view renders even if
Docker is unreachable.

Use --format=json for machine-readable output. --no-live skips the
Docker query. JWTs in credentials are redacted by default; pass
--include-jwt to print them.`,
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
			if err := localnet.ValidateFormat(opts.Format, "table", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunStatus(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Instance to inspect. Can also be passed as a positional argument.")
	cmd.Flags().StringVar(&opts.Format, "format", "table", "Output format: table or json.")
	cmd.Flags().BoolVar(&opts.NoLive, "no-live", false, "Skip docker compose ps; print registry view only.")
	cmd.Flags().BoolVar(&opts.IncludeJWT, "include-jwt", false, "Emit raw JWT values in credentials. Default is <redacted>.")
	return cmd
}
