package cli

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/cli/localnet"
	"github.com/spf13/cobra"
)

func (a *App) buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           appName,
		Short:         "canton-devkit manages Canton LocalNet developer environments.",
		Version:       a.version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				err := fmt.Errorf("unknown command %q", args[0])
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n\n", err)
				_ = cmd.Help()
				return err
			}
			return cmd.Help()
		},
	}
	root.SetOut(a.out)
	root.SetErr(a.err)

	root.AddCommand(a.buildVersionCmd())
	root.AddCommand(localnet.Build())
	return root
}
