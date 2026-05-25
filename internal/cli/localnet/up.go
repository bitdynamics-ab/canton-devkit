package localnet

import (
	"fmt"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	"github.com/spf13/cobra"
)

func buildUp() *cobra.Command {
	opts := &localnet.UpOptions{}
	// Source paths dynamically so the help text reflects whatever
	// CANTON_DEVKIT_REGISTRY override the user has active at help time,
	// and never goes stale when the on-disk layout changes (e.g. the
	// ~/.canton → ~/.canton-devkit rename that prompted Zhe's catch on
	// PR #20).
	cacheRoot := splice.CacheRoot()
	instanceRoot := registry.Root()
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start a Canton LocalNet instance (Splice LocalNet)",
		Long: fmt.Sprintf(`Start a Canton LocalNet instance backed by Splice LocalNet.

The Splice LocalNet compose project is fetched from
https://github.com/canton-network/splice (verified by content-SHA over
the extracted tree) and cached under %s/splice-<tag>/. Per-instance
state lives under %s/<name>/. The host is never modified outside
the parent of those two paths.

Exit codes:
  0  Success
  1  Invalid arguments / unsupported version
  2  Docker preflight failure
  3  Timeout / interrupted
  4  Runtime failure (fetch, compose-up, health check)

Supported Splice versions: %s
"latest" resolves to %s.`,
			cacheRoot, instanceRoot,
			strings.Join(splice.Supported(), ", "), splice.LatestAlias),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(opts.Name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			// TextProgress wraps the Cobra writers so RunUp's typed
			// step events (BIT-163a/b) render as today's terminal
			// lines for CLI users. The Web UI's POST handler
			// (BIT-165) will pass an SSEProgress impl instead so
			// browsers see the full typed event stream.
			prog := &localnet.TextProgress{
				OutW: cmd.OutOrStdout(),
				ErrW: cmd.ErrOrStderr(),
			}
			return localnet.AsExitError(
				localnet.RunUp(cmd.Context(), prog, opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "",
		"Required. Identifier for this LocalNet instance (DNS label: lowercase a-z, 0-9, hyphens; no leading/trailing hyphen; 1-63 chars).")
	cmd.Flags().StringVar(&opts.Version, "version", "latest",
		"Splice LocalNet release tag.")
	cmd.Flags().BoolVar(&opts.AllowUncurated, "allow-uncurated", false,
		"Allow --version tags not in the curated DevKit catalogue. "+
			"DevKit will resolve the tag against the upstream Splice GitHub "+
			"repo and proceed. The resulting LocalNet is not tested by "+
			"DevKit — use for prereleases / alphas at your own risk.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
