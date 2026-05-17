package localnet

import "github.com/spf13/cobra"

func buildRestart() *cobra.Command {
	return newStubCommand("restart", "Restart a Canton LocalNet instance")
}
