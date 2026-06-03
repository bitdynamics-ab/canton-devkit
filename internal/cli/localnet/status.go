package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildStatus() *cobra.Command {
	opts := &localnet.StatusOptions{}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show LocalNet services, endpoints, identities",
		Long: `Reads ~/.canton-devkit/localnet/<name>/state.json and best-effort
docker compose ps output for live service health. The registry view renders
even if Docker is unreachable, so status remains useful during outages.

Use --format=json for the typed Instance shape consumed by scripts and the
Web UI. --no-live skips the Docker query entirely for offline inspection.

JWTs in credentials are redacted by default to <redacted>. Pass --include-jwt
to opt in to raw JWT output.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.Name == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--name is required.\nRun `dpm localnet list` to see available instances.")
				return localnet.AsExitError(localnet.ExitUserError)
			}
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
	cmd.Flags().StringVar(&opts.Name, "name", "", "Required. Instance to inspect.")
	cmd.Flags().StringVar(&opts.Format, "format", "table", "Output format: table or json.")
	cmd.Flags().BoolVar(&opts.NoLive, "no-live", false, "Skip docker compose ps; print registry view only.")
	cmd.Flags().BoolVar(&opts.IncludeJWT, "include-jwt", false, "Emit raw JWT values in credentials. Default is <redacted>.")
	return cmd
}
