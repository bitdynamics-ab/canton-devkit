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
		Short: "Inspect and manage DAR files",
		Long: `Work with DAR files (.dar archives of Daml-LF packages).

Offline (local files):
  info           Inspect a local DAR
  diff           Compare two DARs with SCU upgrade hints

Participant Admin API:
  upload         Upload a DAR
  list           List uploaded DARs or packages
  download       Download a DAR by main package id
  remove         Unvet or remove a DAR
  build-upload   Build via dpm/daml, then upload
  watch          Rebuild and re-upload on source change`,
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
