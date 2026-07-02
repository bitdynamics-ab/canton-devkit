package dar

import (
	"encoding/json"
	"fmt"
	"io"

	cdkdar "github.com/bitdynamics-ab/canton-devkit/internal/dar"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

func buildDiff() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "diff <a.dar> <b.dar>",
		Short: "Compare two DARs with SCU-aware upgrade signals",
		Long: `Diff two DAR files at the package level. Surfaces signals
relevant to Smart Contract Upgrades (SCU):

  - main package name match (mismatch → not an SCU pair)
  - main package version delta (bumped / downgraded / unchanged)
  - main Daml-LF major change (always blocks SCU)
  - dependency packages added / removed / changed

These are **best-effort** signals — authoritative SCU validation lives
with daml damlc and the Ledger API. Use this command to catch obvious
problems early; don't rely on it as the only check.

Exit codes:
  0  Diff completed (regardless of whether changes were found)
  1  Invalid arguments
  4  Either DAR could not be parsed`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			left, err := cdkdar.Open(args[0])
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar diff: open left: %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			right, err := cdkdar.Open(args[1])
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar diff: open right: %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			d := cdkdar.Compare(left, right)
			if format == "json" {
				printDiffJSON(cmd.OutOrStdout(), d)
			} else {
				printDiffText(cmd.OutOrStdout(), d)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	return cmd
}

func printDiffJSON(out io.Writer, d *cdkdar.Diff) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(d)
}

func printDiffText(out io.Writer, d *cdkdar.Diff) {
	_, _ = fmt.Fprintln(out, "Comparing DARs:")
	_, _ = fmt.Fprintf(out, "  Left:  %s\n", d.Left.Path)
	_, _ = fmt.Fprintf(out, "  Right: %s\n", d.Right.Path)
	_, _ = fmt.Fprintln(out)

	// Side-by-side main summary
	_, _ = fmt.Fprintln(out, "Main package:")
	_, _ = fmt.Fprintf(out, "  Name:     %-40s  %s\n", d.Left.MainName, d.Right.MainName)
	_, _ = fmt.Fprintf(out, "  Version:  %-40s  %s\n", orDash(d.Left.MainVersion), orDash(d.Right.MainVersion))
	_, _ = fmt.Fprintf(out, "  LF:       %-40s  %s\n", d.Left.MainLFVersion, d.Right.MainLFVersion)
	_, _ = fmt.Fprintf(out, "  Pkg ID:   %s\n            %s\n", short(d.Left.MainPackageID), short(d.Right.MainPackageID))
	_, _ = fmt.Fprintln(out)

	// SDK
	if d.SDKVersionChanged {
		_, _ = fmt.Fprintf(out, "SDK version changed: %s → %s\n\n", d.Left.SdkVersion, d.Right.SdkVersion)
	} else {
		_, _ = fmt.Fprintf(out, "SDK version: %s (unchanged)\n\n", d.Left.SdkVersion)
	}

	// Dependency deltas
	_, _ = fmt.Fprintf(out, "Dependencies: +%d  -%d  ~%d  (added / removed / changed)\n",
		len(d.AddedPackages), len(d.RemovedPackages), len(d.ChangedPackages))
	if len(d.ChangedPackages) > 0 {
		_, _ = fmt.Fprintln(out, "  Changed:")
		for _, c := range d.ChangedPackages {
			marker := "~"
			if c.LFVersionChanged {
				marker = "!"
			}
			_, _ = fmt.Fprintf(out, "    %s %-40s %s → %s\n",
				marker, truncate(c.Name, 40), orDash(c.LeftVersion), orDash(c.RightVersion))
		}
	}
	if len(d.AddedPackages) > 0 {
		_, _ = fmt.Fprintln(out, "  Added:")
		for _, p := range d.AddedPackages {
			_, _ = fmt.Fprintf(out, "    + %-40s %s\n", truncate(p.Name, 40), orDash(p.Version))
		}
	}
	if len(d.RemovedPackages) > 0 {
		_, _ = fmt.Fprintln(out, "  Removed:")
		for _, p := range d.RemovedPackages {
			_, _ = fmt.Fprintf(out, "    - %-40s %s\n", truncate(p.Name, 40), orDash(p.Version))
		}
	}
	_, _ = fmt.Fprintln(out)

	// SCU signals
	_, _ = fmt.Fprintln(out, "SCU signals (best-effort):")
	for _, s := range d.SCUSignals {
		var prefix string
		switch s.Severity {
		case "block":
			prefix = "  ✗ BLOCK"
		case "warn":
			prefix = "  ⚠ WARN "
		default:
			prefix = "  ℹ INFO "
		}
		_, _ = fmt.Fprintf(out, "%s  %s\n", prefix, s.Message)
	}
}

// orDash returns "-" for empty strings so every text renderer in the
// package prints placeholders identically.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func short(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:16] + "…"
}
