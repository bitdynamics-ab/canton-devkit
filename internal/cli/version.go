package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", appName, a.version)
		},
	}
}
