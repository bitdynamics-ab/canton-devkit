// Package dar wires the `canton-devkit localnet dar` Cobra subtree.
// Subcommands here are thin adapters over internal/dar (which does the
// real parsing). Keeping the wiring separate from the parser means the
// parser is independently testable and reusable from the other dar
// commands still to come (BIT-50/51/53/55).
package dar

import (
	"github.com/spf13/cobra"
)

// Build returns the `dar` Cobra command, registered by
// internal/cli/localnet/localnet.go.
//
// Subcommands currently implemented:
//   - `info` (BIT-52, file variant) — inspect a local .dar
//   - `diff` (BIT-54)               — compare two local .dars
//
// Subcommands still to come (BIT-50/51/53/55) get added here as their
// PRs land. Each is its own file in this package.
func Build() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dar",
		Short: "Inspect and manage DAR files (Daml package archives)",
		Long: `Tools for working with DAR files: the .dar zip archives that
package Daml-LF compiled code for deployment to a Canton ledger.

Available commands:
  info    Inspect a local DAR and show its packages, LF version, deps
  diff    Compare two DAR files with SCU-aware upgrade signals

Future commands (upload/list/download/remove) will reach a participant's
Admin API; the inspection commands here work entirely offline.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(buildInfo())
	cmd.AddCommand(buildDiff())
	return cmd
}
