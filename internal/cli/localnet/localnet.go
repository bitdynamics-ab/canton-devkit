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

	// Inspection — real implementations landed on main via
	// (status, list), (creds, logs),
	// (doctor), (env).
	localnet.AddCommand(buildStatus())
	localnet.AddCommand(buildList())
	localnet.AddCommand(buildEnv())
	localnet.AddCommand(buildDoctor())
	localnet.AddCommand(buildLogs())
	localnet.AddCommand(buildCreds())

	// — snapshot/restore. Source-copied from
	// srikanth/bit-147-p1-10; wires UI parity on top.
	localnet.AddCommand(buildSnapshot())
	localnet.AddCommand(buildRestore())

	localnet.AddCommand(buildVersions())
	localnet.AddCommand(buildUI())

	// — AI-agent skill docs. Same embedded docs back the
	// Web UI Agent Skills screen .
	localnet.AddCommand(buildSkills())

	// CLI ↔ Web UI parity (see AGENTS.md): every per-container
	// HTTP endpoint has a CLI mirror under `container <verb>`,
	// and the on-demand reconciler runs via `refresh`.
	localnet.AddCommand(buildContainer())
	localnet.AddCommand(buildRefresh())
	localnet.AddCommand(buildMetrics())

	// contracts/tx CLI — uses internal/canton/ledger
	// from PR #66. Endpoint discovery (auto-resolving the
	// participant gRPC port from registry state) is a follow-up;
	// callers pass --endpoint host:port for now.
	localnet.AddCommand(buildContracts())
	localnet.AddCommand(buildTx())

	// DAR admin commands (upload/list/download/info/diff/
	// remove/build-upload/watch/connect) — adopted from the
	// `srikanth/bit-track-b-dar-admin` branch.
	localnet.AddCommand(dar.Build())

	// Token Standard V2 commands (create wizard +
	// mint/transfer/burn/balance). Mirrors dar.Build()'s pattern —
	// the token subpackage owns its own command tree.
	localnet.AddCommand(token.Build())

	return localnet
}
