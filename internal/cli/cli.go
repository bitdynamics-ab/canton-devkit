package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const appName = "canton-devkit"

// App owns the CLI dependencies so tests can capture output without touching
// process-global stdout, stderr, or os.Exit.
type App struct {
	out     io.Writer
	err     io.Writer
	version string
}

func New(out io.Writer, err io.Writer, version string) *App {
	return &App{out: out, err: err, version: version}
}

func (a *App) Run(args []string) int {
	root := a.buildRoot()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return 1
	}
	return 0
}

func (a *App) buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           appName,
		Short:         "canton-devkit manages Canton LocalNet developer environments.",
		Version:       a.version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: cobra.ArbitraryArgs,
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
	root.AddCommand(a.buildLocalnet())
	return root
}

func (a *App) buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", appName, a.version)
		},
	}
}

func (a *App) buildLocalnet() *cobra.Command {
	localnet := &cobra.Command{
		Use:   "localnet",
		Short: "Manage Canton LocalNet instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "unknown localnet command %q\n\n", args[0])
				_ = cmd.Help()
				return fmt.Errorf("unknown localnet command %q", args[0])
			}
			return cmd.Help()
		},
	}

	subcommands := []struct {
		use   string
		short string
	}{
		{"up", "Start a Canton LocalNet instance"},
		{"down", "Stop a Canton LocalNet instance"},
		{"restart", "Restart a Canton LocalNet instance"},
		{"clean", "Remove Canton LocalNet state"},
		{"status", "Show Canton LocalNet status"},
		{"logs", "Show Canton LocalNet logs"},
	}

	for _, sc := range subcommands {
		sc := sc
		localnet.AddCommand(&cobra.Command{
			Use:                sc.use,
			Short:              sc.short,
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "localnet %s: %s is not implemented yet\n", sc.use, sc.short)
				return err
			},
		})
	}

	return localnet
}
