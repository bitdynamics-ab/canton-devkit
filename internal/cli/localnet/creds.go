package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildCreds() *cobra.Command {
	opts := &localnet.CredsOptions{Format: "table"}
	cmd := &cobra.Command{
		Use:   "creds [name]",
		Short: "Print captured Splice JWTs for a LocalNet instance",
		Long: `JWTs are HS256-signed with the documented dev-only "unsafe" secret
and are valid for the lifetime of the instance. They are captured at
` + "`localnet up`" + ` time and persisted in state.json (mode 0600).

Formats:
  table (default) — human-readable summary including JWTs
  env             — export AUTH_<ROLE>_TOKEN=... lines for eval $(...)
  json            — array of credential objects (JWTs included)
  raw             — single JWT (requires --role)`,
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
			if err := localnet.ValidateCredsOptions(opts); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunCreds(cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "Instance name. Can also be passed as a positional argument.")
	cmd.Flags().StringVar(&opts.Role, "role", "", "Restrict to one role (sv, app-provider, app-user); omit for all.")
	cmd.Flags().StringVar(&opts.Format, "format", "table", "Output format: table, env, json, or raw.")
	return cmd
}
