package localnet

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	"github.com/spf13/cobra"
)

// buildVersions returns the `localnet versions` Cobra command. It lists
// every Splice version the catalogue knows about plus every tag the
// upstream repository currently exposes, with a status flag per row:
//
//   - supported      → DevKit catalogues it, upstream pin matches.
//   - drifted        → DevKit catalogues it, upstream tag was force-
//     moved to a different commit. Security signal.
//   - available      → upstream has it; DevKit doesn't catalogue it
//     yet. Run scripts/add-splice-version.sh to add.
//   - catalogued-only → DevKit catalogues it; upstream no longer has
//     it (tag was deleted). Investigate before
//     removing.
//
// Network is optional: if `--offline` is passed or the network call
// fails, we render the catalogue alone and print a friendly note.
func buildVersions() *cobra.Command {
	var offline bool
	var format string

	cmd := &cobra.Command{
		Use:   "versions",
		Short: "List Splice versions supported by this DevKit + upstream",
		Long: `Lists every Splice version DevKit's curated catalogue knows about
plus every tag the upstream canton-network/splice repository currently
exposes.

Per-row status:
  supported       — catalogued; upstream pin matches.
  drifted         — catalogued; upstream tag was force-moved (security
                    signal — re-review the catalogue entry).
  available       — upstream has it; not yet in our catalogue.
                    Maintainers: scripts/add-splice-version.sh <tag>.
  catalogued-only — we still catalogue it; upstream no longer has it.

Pass --offline to skip the upstream lookup and print catalogue only.
Format flags: --format=text (default) or --format=json.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet_validateFormat(format, "text", "json"); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			ctx := cmd.Context()

			if offline {
				return renderCatalogueOnly(out, format)
			}
			rows, err := splice.CrossReferenceUpstream(ctx)
			if err != nil {
				// Network failure: degrade gracefully. Print catalogue
				// and warn the user.
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: upstream lookup failed (%v); showing catalogue only. Use --offline to silence.\n",
					err)
				return renderCatalogueOnly(out, format)
			}
			return renderVersions(out, rows, format)
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "skip upstream GitHub lookup; show catalogue only")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

func renderCatalogueOnly(out cobraWriter, format string) error {
	rows := make([]splice.VersionStatus, 0, len(splice.SupportedVersions))
	for tag, v := range splice.SupportedVersions {
		rows = append(rows, splice.VersionStatus{
			Tag:              tag,
			Status:           splice.StatusSupported, // best we know without network
			CataloguedCommit: v.Commit,
			Major:            v.Major,
		})
	}
	return renderVersions(out, rows, format)
}

func renderVersions(out cobraWriter, rows []splice.VersionStatus, format string) error {
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	// Text format: aligned table.
	_, _ = fmt.Fprintf(out, "%-16s  %-15s  %-10s  %-40s  %s\n",
		"TAG", "STATUS", "MAJOR", "COMMIT", "NOTE")
	for _, r := range rows {
		commit := r.CataloguedCommit
		note := ""
		switch r.Status {
		case splice.StatusDrifted:
			note = fmt.Sprintf("upstream now %s", short(r.UpstreamCommit))
		case splice.StatusAvailable:
			commit = r.UpstreamCommit
			note = "run scripts/add-splice-version.sh " + r.Tag
		}
		_, _ = fmt.Fprintf(out, "%-16s  %-15s  %-10s  %-40s  %s\n",
			r.Tag, r.Status.String(), r.Major, short(commit), note)
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// cobraWriter is io.Writer minus the dependency cycle when this file
// hand-rolls its own writer. We accept anything that can write bytes.
type cobraWriter interface {
	Write(p []byte) (n int, err error)
}

// localnet_validateFormat keeps this command's format check independent
// of the localnet package's identical helper — PR-A lands this before
// PR-B exposes ValidateFormat publicly. Cheap defensive copy.
func localnet_validateFormat(got string, allowed ...string) error {
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return fmt.Errorf("--format must be one of %s (got %q)", strings.Join(allowed, ","), got)
}
