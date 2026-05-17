package localnet

import "github.com/spf13/cobra"

func buildDown() *cobra.Command {
	return newStubCommand("down", "Stop a Canton LocalNet instance")
}
