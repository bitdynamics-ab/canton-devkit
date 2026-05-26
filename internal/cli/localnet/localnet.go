package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/cli/localnet/dar"
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

	localnet.AddCommand(buildUp())
	localnet.AddCommand(buildDown())
	localnet.AddCommand(buildRestart())
	localnet.AddCommand(buildClean())
	localnet.AddCommand(buildStatus()) // BIT-144 wedge; PR #38 replaces wholesale
	localnet.AddCommand(buildList())   // BIT-146 wedge; PR #32 replaces wholesale
	localnet.AddCommand(buildEnv())    // BIT-125 wedge; PR #34 replaces wholesale
	localnet.AddCommand(buildDoctor()) // BIT-123 wedge; PR #39 replaces wholesale
	localnet.AddCommand(buildLogs())
	localnet.AddCommand(buildVersions())
	localnet.AddCommand(buildUI())
	// CLI ↔ Web UI parity (see AGENTS.md): every per-container
	// HTTP endpoint has a CLI mirror under `container <verb>`,
	// and the on-demand reconciler runs via `refresh`.
	localnet.AddCommand(buildContainer())
	localnet.AddCommand(buildRefresh())
	localnet.AddCommand(buildMetrics())
	// BIT-136: contracts/tx CLI — uses internal/canton/ledger
	// from PR #66. Endpoint discovery (auto-resolving the
	// participant gRPC port from registry state) is a follow-up;
	// callers pass --endpoint host:port for now.
	localnet.AddCommand(buildContracts())
	localnet.AddCommand(buildTx())
	// BIT-127: DAR admin commands (upload/list/download/info/diff/
	// remove/build-upload/watch/connect) — adopted from the
	// `srikanth/bit-track-b-dar-admin` branch. Re-skinning against
	// `screens-dar.jsx` mockups stays on this ticket as a follow-up
	// polish pass; the functional commands land here first so the
	// Web UI DAR Manager can consume them.
	localnet.AddCommand(dar.Build())
	return localnet
}

func newStubCommand(use string, short string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "localnet %s: %s is not implemented yet\n", use, short)
			return err
		},
	}
}
