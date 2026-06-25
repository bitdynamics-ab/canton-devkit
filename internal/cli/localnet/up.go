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
	// and never goes stale when the on-disk layout changes.
	cacheRoot := splice.CacheRoot()
	instanceRoot := registry.Root()
	cmd := &cobra.Command{
		Use:     "up [name]",
		Aliases: []string{"start"},
		Short:   "Start a LocalNet instance",
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
			// TextProgress wraps the Cobra writers so RunUp's typed
			// step events render as terminal lines for CLI users.
			// The Web UI's POST handler passes an SSEProgress impl
			// instead so browsers see the full typed event stream.
			//
			// NewTextProgress auto-detects whether the writer is a
			// TTY and switches between the styled rendering (Section
			// headers, brand-accented Box for the success marker)
			// and plain text (pipes, CI, golden-byte tests). The
			// literal struct form is kept for backward
			// compatibility — tests that constructed TextProgress
			// directly still get the plain bytes they assert on.
			prog := localnet.NewTextProgress(
				cmd.OutOrStdout(), cmd.ErrOrStderr())
			return localnet.AsExitError(
				localnet.RunUp(cmd.Context(), prog, opts))
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "",
		"Identifier for this LocalNet instance (DNS label: lowercase a-z, 0-9, hyphens; no leading/trailing hyphen; 1-63 chars). Can also be passed as a positional argument.")
	cmd.Flags().StringVar(&opts.Version, "version", "latest",
		"Splice LocalNet release tag.")
	cmd.Flags().BoolVar(&opts.AllowUncurated, "allow-uncurated", false,
		"Allow --version tags not in the curated DevKit catalogue. "+
			"DevKit will resolve the tag against the upstream Splice GitHub "+
			"repo and proceed. The resulting LocalNet is not tested by "+
			"DevKit — use for prereleases / alphas at your own risk.")
	cmd.Flags().StringSliceVar(&opts.Profiles, "profile", nil,
		"Docker compose profiles to enable. "+
			"`--profile prometheus` and `--profile grafana` can be enabled "+
			"independently to control each observability sidecar separately; "+
			"`--profile observability` (legacy umbrella) activates both at once. "+
			"Each adds ~250-350 MiB; host ports are allocated and persisted so "+
			"re-up preserves bookmarked URLs. Repeatable for multiple profiles.")
	cmd.Flags().IntVar(&opts.PortBase, "port-base", 0,
		"Pin host ports deterministically from this base instead of auto-allocating "+
			"(e.g. --port-base 20000 → each service gets base+N). Use for reproducible "+
			"multi-instance or CI layouts; every derived port must be free or `up` fails "+
			"fast (no silent fallback). 0 (default) = auto-allocate with stable reuse.")
	return cmd
}
