package localnet

import "github.com/spf13/cobra"

func buildClean() *cobra.Command {
	return newStubCommand("clean", "Remove Canton LocalNet state")
}
