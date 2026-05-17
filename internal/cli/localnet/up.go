package localnet

import "github.com/spf13/cobra"

func buildUp() *cobra.Command {
	return newStubCommand("up", "Start a Canton LocalNet instance")
}
