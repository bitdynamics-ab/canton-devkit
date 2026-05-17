package localnet

import "github.com/spf13/cobra"

func buildStatus() *cobra.Command {
	return newStubCommand("status", "Show Canton LocalNet status")
}
