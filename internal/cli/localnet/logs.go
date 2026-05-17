package localnet

import "github.com/spf13/cobra"

func buildLogs() *cobra.Command {
	return newStubCommand("logs", "Show Canton LocalNet logs")
}
