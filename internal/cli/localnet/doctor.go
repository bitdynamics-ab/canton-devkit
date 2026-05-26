package localnet

import (
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// `localnet doctor` runs host preflight checks (Docker CLI / daemon /
// Compose v2 / memory / disk / host prereqs) and reports a per-check
// pass/warn/fail grid with remediation tips on failure.
//
// This is the v1 wedge: it reuses internal/docker.RunPreflight (which
// already exists and is tested) so users get a working doctor command
// today. The richer version in PR #39 (BIT-123) adds the proposal's
// section grouping (System / Resources / Network), --format json, and
// the threshold-parity test that pins doctor's thresholds to up's.
// When PR #39 lands, this wedge is replaced wholesale.
//
// Exit codes (stable per proposal):
//
//	0 — every check passed (warnings are advisory; exit 0)
//	2 — at least one FAIL ("preflight failure")
func buildDoctor() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run host preflight checks (Docker, memory, disk, prereqs)",
		Long: "Runs the same preflight check set as `localnet up` and " +
			"reports each result with remediation tips on failure. " +
			"Exit code 0 if all pass, 2 if any check FAILs. WARN-only " +
			"reports still exit 0 — warnings are advisory.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := docker.RunPreflight(cmd.Context(), docker.Options{
				DataDir:        registry.Root(),
				MinDiskBytes:   docker.DefaultMinDiskBytes,
				MinMemoryBytes: docker.DefaultMinMemoryBytes,
			})
			renderDoctor(cmd.OutOrStdout(), report)
			if !report.OK() {
				// Return a typed error so the dispatcher can extract
				// the exit code via errors.As (per CLAUDE.md: exit
				// codes must travel via errors.As, not raw integers).
				return doctorFailureError{}
			}
			return nil
		},
	}
	return cmd
}

// renderDoctor walks the report and prints one row per check.
func renderDoctor(out io.Writer, r *docker.Report) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, term.Section("DOCTOR", "", "", 0))
	hasWarn := false
	for _, c := range r.Results {
		var glyph string
		switch c.Status {
		case docker.StatusOK:
			glyph = term.Successc("✓")
		case docker.StatusWarn:
			glyph = term.Warnc("!")
			hasWarn = true
		case docker.StatusFail:
			glyph = term.Errorc("✗")
		case docker.StatusSkipped:
			glyph = term.Dimc("·")
		default:
			glyph = " "
		}
		line := fmt.Sprintf("%s  %s", glyph, c.Name)
		if c.Detail != "" {
			line += "  " + term.Dimc(c.Detail)
		}
		fmt.Fprintln(out, line)
		if c.Remediation != "" && (c.Status == docker.StatusFail || c.Status == docker.StatusWarn) {
			fmt.Fprintln(out, "     "+term.Dimc("→ "+c.Remediation))
		}
	}
	fmt.Fprintln(out)
	switch {
	case !r.OK():
		fmt.Fprintln(out, term.Errorc("Preflight FAILED — fix the FAILs above before `localnet up`."))
	case hasWarn:
		fmt.Fprintln(out, term.Warnc("Preflight passed with warnings — `localnet up` should still work."))
	default:
		fmt.Fprintln(out, term.Successc("All checks passed."))
	}
	fmt.Fprintln(out)
}

// doctorFailureError carries the documented exit code 2
// (ExitPreflightFail) through cobra's RunE return so scripts can rely
// on the matrix. The CLI dispatcher in cmd/canton-devkit extracts the
// code via errors.As — same pattern as internal/localnet.ExitCodeError
// but kept package-local to avoid the cyclic dep.
type doctorFailureError struct{}

func (doctorFailureError) Error() string { return "doctor: preflight failure" }
func (doctorFailureError) ExitCode() int { return 2 }
