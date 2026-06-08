package localnet

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// — `dpm localnet doctor`.
//
// Renders the System / Resources / Network sections from
// docs/design/mockups/screens-lifecycle.jsx (ScreenDoctor) by
// translating docker.RunPreflight output. The same Report is also
// surfaced as types.PreflightReport via --format=json so the Web
// UI can call CollectDoctor and render
// the same data.
//
// The check set lives in internal/localnet.CollectDoctor; this file is only the
// CLI surface — flags + rendering + JSON.

var doctorCollectFn = localnet.CollectDoctor

func buildDoctor() *cobra.Command {
	var format string
	var version string
	var portBase int
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check host readiness for LocalNet (docker, resources, network)",
		Long: `Runs the same preflight checks ` + "`localnet up`" + ` runs before
bringing up a new instance — Docker CLI, daemon reachability,
Compose v2, disk + memory headroom — but reports them in a
diagnostic format with remediation hints for the failures.

Use this before filing a bug or after a fresh OS reinstall.
Exits 0 on all-pass, 2 on any FAIL (matches localnet up's
ExitPreflightFail semantics).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch format {
			case "", "table", "json":
			default:
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"--format must be table or json (got %q)\n", format)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			rep, err := doctorCollectFn(cmd.Context(), localnet.DoctorOptions{Version: version, PortBase: portBase})
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			switch format {
			case "", "table":
				writeDoctorTable(cmd.OutOrStdout(), rep)
			case "json":
				if err := writeDoctorJSON(cmd.OutOrStdout(), rep); err != nil {
					// Print the underlying error so partial JSON writes are diagnosable.
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"write JSON output: %s\n", err)
					return localnet.AsExitError(localnet.ExitRuntimeFailure)
				}
			}
			if !rep.OK {
				// Anonymous telemetry (no-op unless enabled): which checks
				// failed, by stable slug. Only the check id is recorded.
				for _, sec := range rep.Sections {
					for _, ch := range sec.Checks {
						if ch.Result == "fail" {
							telemetry.Inc("dpm/doctor_fail", telemetry.Slug(ch.Label))
						}
					}
				}
				return localnet.AsExitError(localnet.ExitPreflightFail)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table | json")
	cmd.Flags().StringVar(&version, "version", "latest", "Splice version used for memory thresholds")
	cmd.Flags().IntVar(&portBase, "port-base", 0,
		"Check the FIXED host-port block `up --port-base <N>` would claim (N..N+services) instead of ephemeral-port availability. Use to pre-flight a deterministic-port layout.")
	return cmd
}

// writeDoctorTable renders ScreenDoctor: one Section per category
// with Step rows for each check + a colored summary Box.
func writeDoctorTable(w io.Writer, rep types.PreflightReport) {
	_, _ = fmt.Fprintln(w, term.Dimc("Checking host readiness for Canton LocalNet…"))
	_, _ = fmt.Fprintln(w)

	// Reviewer pin (PR #39 #4): the original renderer emitted a
	// loose stack of Step rows per section. Sections with one
	// check looked indistinguishable from prose, and the columns
	// (status glyph / label / detail) didn't align across sections
	// because Step is whitespace-padded, not column-aligned. We
	// now render each section as its own term.Table with a fixed
	// column layout (status · check · detail) so the eye can
	// scan vertically. Section headers stay so users can tell
	// System checks from Resources checks at a glance.
	for _, sec := range rep.Sections {
		rows := make([][]string, 0, len(sec.Checks))
		for _, c := range sec.Checks {
			rows = append(rows, []string{
				stepGlyph(c.Result),
				term.Textc(c.Label),
				term.Dimc(c.Detail),
			})
		}
		body := term.Table([]term.Column{
			{Label: ""}, // glyph column — header empty
			{Label: "check"},
			{Label: "detail"},
		}, rows)
		_, _ = fmt.Fprintln(w, term.Section(sec.Title, "", body, 0))
		_, _ = fmt.Fprintln(w)
	}

	// Per-section remediation block on failure/warning checks.
	// We surface remediation as a friendly Box at the END so the
	// scan-table-then-act flow matches the JSX mockup.
	//
	// Reviewer pin (PR #39 #4 round-4): the previous shape
	// flattened multi-step remediations by joining them with " · "
	// into one line per check — so a three-step recovery (e.g.
	// "free port, retry up, run doctor") rendered as a single
	// dense Step row that visually fused unrelated actions. We now
	// keep each remediation step on its own row under the check
	// label, like a sub-list. Each (label, [steps]) pair becomes
	// one labelled header + N indented step rows.
	type remediationBlock struct {
		label string
		steps []string
	}
	var remediations []remediationBlock
	worstKind := term.BoxBrand
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			if len(c.Remediation) == 0 {
				continue
			}
			remediations = append(remediations, remediationBlock{
				label: c.Label,
				steps: c.Remediation,
			})
			switch c.Result {
			case "fail":
				worstKind = term.BoxError
			case "warn":
				if worstKind != term.BoxError {
					worstKind = term.BoxWarn
				}
			}
		}
	}
	if rep.OK && len(remediations) == 0 {
		_, _ = fmt.Fprintln(w, term.Box(term.BoxBrand,
			fmt.Sprintf("%s %s", term.Brandc("✦"), term.Textc(rep.Summary))))
		return
	}
	var body strings.Builder
	body.WriteString(term.Textc(rep.Summary))
	if len(remediations) > 0 {
		body.WriteString("\n\n")
		body.WriteString(term.Dimc("Remediation:"))
		body.WriteString("\n")
		for i, r := range remediations {
			// Header row: check label, with the warn glyph.
			body.WriteString(term.Step(term.StepWarn, r.label, "", ""))
			body.WriteString("\n")
			// Sub-rows: one indented numbered step per remediation
			// entry. Numbering helps scripts and humans refer to a
			// specific step (e.g. "step 2 above").
			for j, s := range r.steps {
				body.WriteString("     ")
				body.WriteString(term.Brandc(fmt.Sprintf("%d.", j+1)))
				body.WriteString(" ")
				body.WriteString(term.Textc(s))
				body.WriteString("\n")
			}
			// Blank line between blocks for visual separation.
			if i < len(remediations)-1 {
				body.WriteString("\n")
			}
		}
	}
	_, _ = fmt.Fprintln(w, term.Box(worstKind, body.String()))
}

// stepGlyph renders a single-cell colored glyph for the column
// layout: pass=✓ (success), warn=⚠ (warn), fail=✗ (error),
// skip=○ (dim). Mirrors stepKindFor but emits a one-cell string
// suitable for term.Table's first column instead of a full Step
// row.
func stepGlyph(result string) string {
	switch result {
	case "pass":
		return term.Successc(term.Glyphs.Check)
	case "warn":
		return term.Warnc(term.Glyphs.Warn)
	case "fail":
		return term.Errorc(term.Glyphs.Cross)
	case "skip":
		return term.Dimc(term.Glyphs.Pending)
	}
	return term.Dimc(term.Glyphs.Pending)
}

// writeDoctorJSON serialises the report with stable indentation.
func writeDoctorJSON(w io.Writer, rep types.PreflightReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}
