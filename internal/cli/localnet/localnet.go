package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/cli/localnet/dar"
	"github.com/bitdynamics-ab/canton-devkit/internal/cli/localnet/token"
	"github.com/spf13/cobra"
)

func Build() *cobra.Command {
	localnet := &cobra.Command{
		Use:   "localnet",
		Short: "Manage Canton LocalNet instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				_ = cmd.Help()
				return fmt.Errorf("unknown localnet command %q", args[0])
			}
			return cmd.Help()
		},
	}

	// Lifecycle.
	localnet.AddCommand(buildUp())
	localnet.AddCommand(buildDown())
	localnet.AddCommand(buildRestart())
	localnet.AddCommand(buildPause())
	localnet.AddCommand(buildResume())
	localnet.AddCommand(buildClean())

	// Inspection.
	localnet.AddCommand(buildStatus())
	localnet.AddCommand(buildList())
	localnet.AddCommand(buildEnv())
	localnet.AddCommand(buildDoctor())
	localnet.AddCommand(buildLogs())
	localnet.AddCommand(buildCreds())

	localnet.AddCommand(buildSnapshot())
	localnet.AddCommand(buildRestore())

	localnet.AddCommand(buildVersions())
	localnet.AddCommand(buildUI())

	// AI-agent skill docs. The same embedded docs back the
	// Web UI Agent Skills screen.
	localnet.AddCommand(buildSkills())

	// CLI ↔ Web UI parity (see CONTRIBUTING.md): every per-container
	// HTTP endpoint has a CLI mirror under `container <verb>`,
	// and the on-demand reconciler runs via `refresh`.
	localnet.AddCommand(buildContainer())
	localnet.AddCommand(buildRefresh())
	localnet.AddCommand(buildMetrics())
	localnet.AddCommand(buildObservability())

	localnet.AddCommand(buildContracts())
	localnet.AddCommand(buildTx())

	// DAR admin and Token Standard commands live in their own
	// subpackages, each owning its command tree.
	localnet.AddCommand(dar.Build())
	localnet.AddCommand(token.Build())

	return localnet
}
