package dar

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/bitdynamics-ab/canton-devkit/internal/analyzer"
	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

// dar analyze — run the vendored daml-analyzer on a local .dar and
// report its cross-package interactions. Offline / file-based: no
// participant involved, so no connection flags.
func buildAnalyze() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "analyze <path-to-dar>",
		Short: "Analyze a local DAR's cross-package interactions",
		Long: `Run the daml-analyzer on a local .dar and report which templates,
interfaces, and choices it reaches across package boundaries.

Requires Docker. The pinned analyzer image is pulled on first use;
override it with DAML_ANALYZER_IMAGE.

Exit codes:
  0  Analysis returned
  1  Invalid arguments, or Docker not available
  4  Analyzer run failed`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			path := args[0]
			rep, err := analyzer.AnalyzeDAR(cmd.Context(), path)
			if err != nil {
				if errors.Is(err, analyzer.ErrDockerNotFound) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"dar analyze: Docker not found. Install Docker to run the analyzer image.")
					return localnet.AsExitError(localnet.ExitUserError)
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar analyze: %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}

			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), types.AnalyzerResponse{
					SchemaVersion: types.SchemaVersion,
					DarName:       filepath.Base(path),
					Report:        rep,
				})
			}
			printAnalyzerReport(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	return cmd
}

// printAnalyzerReport renders a human-readable summary: analyzed
// package, dependency count, interaction totals, then one line per
// interaction.
func printAnalyzerReport(out io.Writer, rep *types.AnalyzerReport) {
	if rep == nil {
		_, _ = fmt.Fprintln(out, "No report.")
		return
	}
	p := rep.AnalyzedPackage
	lf := p.LFVersion
	if lf == "" {
		lf = "?"
	}
	_, _ = fmt.Fprintf(out, "Package:      %s %s (LF %s)\n", p.Name, p.Version, lf)
	_, _ = fmt.Fprintf(out, "Dependencies: %d\n", len(rep.Dependencies))
	_, _ = fmt.Fprintf(out, "Interactions: %d\n", rep.Summary.TotalInteractions)

	for _, k := range sortedKeys(rep.Summary.ByType) {
		_, _ = fmt.Fprintf(out, "  %-20s %d\n", k, rep.Summary.ByType[k])
	}

	if len(rep.Interactions) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nInteractions:")
	for _, it := range rep.Interactions {
		_, _ = fmt.Fprintf(out, "  %-20s %s -> %s\n",
			it.Type, endpointLabel(it.Caller), endpointLabel(it.Target))
	}
}

// endpointLabel renders one side of an interaction as
// "<module>.<template|choice|interface>", preferring the most specific
// member the endpoint carries.
func endpointLabel(e types.AnalyzerEndpoint) string {
	member := ""
	switch {
	case e.Choice != "":
		member = e.Choice
	case e.Template != "":
		member = e.Template
	case e.Interface != "":
		member = e.Interface
	}
	if member == "" {
		return e.Module
	}
	return e.Module + "." + member
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
