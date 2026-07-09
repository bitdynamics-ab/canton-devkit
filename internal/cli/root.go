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
		Version:       a.versionString(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				_ = cmd.Help()
				return fmt.Errorf("unknown command %q", args[0])
			}
			return cmd.Help()
		},
	}
	root.SetOut(a.out)
	root.SetErr(a.err)

	// Defense-in-depth: Run already strips the DPM marker before Cobra
	// parses, but register it as a hidden persistent flag so a stray
	// `--via-dpm` reaching the parser is tolerated rather than erroring.
	var viaDPM bool
	root.PersistentFlags().BoolVar(&viaDPM, "via-dpm", false, "")
	_ = root.PersistentFlags().MarkHidden("via-dpm")

	root.AddCommand(a.buildVersionCmd())
	root.AddCommand(buildTelemetryCmd()) // root-level: telemetry is tool-wide, not a LocalNet concern
	ln := localnet.Build(a.mode == ViaDPM)
	applyHelp(ln, a.mode.displayName()) // custom boxed/sectioned template for `localnet --help`
	root.AddCommand(ln)
	return root
}
