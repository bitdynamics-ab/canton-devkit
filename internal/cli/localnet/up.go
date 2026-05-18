package localnet

import (
	"fmt"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	"github.com/spf13/cobra"
)

func buildUp() *cobra.Command {
	opts := &localnet.UpOptions{}
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start a Canton LocalNet instance (Splice LocalNet)",
		Long: fmt.Sprintf(`Start a Canton LocalNet instance backed by Splice LocalNet.

The Splice LocalNet compose project is fetched from
https://github.com/canton-network/splice (verified by SHA256) and cached
under ~/.canton/devkit-cache/splice-<tag>/. The host is never modified
outside ~/.canton/.

Exit codes:
  0  Success
  1  Invalid arguments / unsupported version
  2  Docker preflight failure
  3  Timeout / interrupted
  4  Runtime failure (fetch, compose-up, health check)

Supported Splice versions: %s
"latest" resolves to %s.`,
			strings.Join(splice.Supported(), ", "), splice.LatestAlias),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(opts.Name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				localnet.RunUp(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "",
		"Required. Identifier for this LocalNet instance (alphanumeric + hyphens, 1-63 chars).")
	cmd.Flags().StringVar(&opts.Version, "version", "latest",
		"Splice LocalNet release tag.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
