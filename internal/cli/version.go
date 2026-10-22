package cli

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/campaign"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

func (a *App) buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", appName, a.versionString())
			code, err := campaign.Lookup()
			if code != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "campaign code: %s\n", code)
			}
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), term.Warnc("warning: "+err.Error()))
			}
		},
	}
}
