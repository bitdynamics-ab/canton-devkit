package cli

import (
	"fmt"
	"io"

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
			printCampaignCode(cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func printCampaignCode(out, errOut io.Writer) {
	code, err := campaign.Lookup()
	if code != "" {
		_, _ = fmt.Fprintf(out, "campaign code: %s\n", code)
	}
	if err != nil {
		_, _ = fmt.Fprintln(errOut, term.Warnc("warning: "+err.Error()))
	}
}

func printedVersionFlag(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Version == "" {
		return false
	}
	help, _ := cmd.Flags().GetBool("help")
	version, _ := cmd.Flags().GetBool("version")
	return version && !help
}
