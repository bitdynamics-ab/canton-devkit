// Package dar wires the `canton-devkit localnet dar` Cobra subtree.
// Subcommands here are thin adapters over internal/dar (which does the
// real parsing). Keeping the wiring separate from the parser means the
// parser is independently testable and reusable from the other dar
// commands still to come .
package dar

import (
	"github.com/spf13/cobra"
)

// Build returns the `dar` Cobra command, registered by
// internal/cli/localnet/localnet.go.
//
// Subcommands currently implemented:
// - `info` — inspect a local .dar
// - `diff` — compare two local .dars
//
// Subcommands still to come get added here as their
// PRs land. Each is its own file in this package.
func Build() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dar",
		Short: "Inspect and manage DAR files (Daml package archives)",
		Long: `Tools for working with DAR files: the .dar zip archives that
package Daml-LF compiled code for deployment to a Canton ledger.

Available commands (offline, file-based):
  info           Inspect a local DAR and show its packages, LF version, deps
  diff           Compare two DAR files with SCU-aware upgrade signals

Available commands (participant Admin API):
  upload         Upload a DAR to a participant
  list           List uploaded DARs / packages on a participant
  download       Download a DAR back from a participant by main package id
  remove         Unvet (or fully purge) a DAR on a participant
  build-upload   Shell out to dpm/daml build, then upload
  watch          Rebuild + re-upload on source change (hot-deploy loop)`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(buildInfo())
	cmd.AddCommand(buildDiff())
	cmd.AddCommand(buildUpload())
	cmd.AddCommand(buildListUploaded())
	cmd.AddCommand(buildDownload())
	cmd.AddCommand(buildRemove())
	cmd.AddCommand(buildBuildUpload())
	cmd.AddCommand(buildWatch())
	return cmd
}
