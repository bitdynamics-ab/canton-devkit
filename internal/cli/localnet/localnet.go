package localnet

import (
	"fmt"

	"github.com/spf13/cobra"
)

func Build() *cobra.Command {
	localnet := &cobra.Command{
		Use:   "localnet",
		Short: "Manage Canton LocalNet instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				_ = cmd.Help()
				return fmt.Errorf("unknown localnet command %q", args[0])
			}
			return cmd.Help()
		},
	}

	localnet.AddCommand(buildUp())
	localnet.AddCommand(buildDown())
	localnet.AddCommand(buildRestart())
	localnet.AddCommand(buildClean())
	localnet.AddCommand(buildStatus())
	localnet.AddCommand(buildLogs())
	localnet.AddCommand(buildVersions())
	localnet.AddCommand(buildList())
	localnet.AddCommand(buildCreds())
	localnet.AddCommand(buildDoctor())
	return localnet
}

func newStubCommand(use string, short string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "localnet %s: %s is not implemented yet\n", use, short)
			return err
		},
	}
}
