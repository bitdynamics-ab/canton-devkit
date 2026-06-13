package dar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/darops"
	"github.com/spf13/cobra"
)

// dar list — list uploaded DARs / packages on a participant.
//
// The Canton Admin API exposes two related views:
//
//	ListDars → uploaded archives (.dar files), addressed by main_package_id
//	ListPackages → individual .dalf packages inside DARs
//
// `dar list` defaults to ListDars (one row per uploaded DAR — the usual
// "what have I deployed?" question). Pass --packages to switch to the
// per-package view (useful when chasing down a missing transitive dep).
//
// `--enrich` adds Daml-LF version + module count to each row by calling
// GetPackageContents once per package. Surfaces what the proposal
// specifies as the default `dar list` output but kept behind a flag
// since the per-row RPCs add latency proportional to the package count.
func buildListUploaded() *cobra.Command {
	var (
		conn       connectFlags
		format     string
		packages   bool
		enrich     bool
		vetting    bool
		filterName string
		limit      int32
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List uploaded DARs (or packages with --packages) on a participant",
		Args:  cobra.NoArgs,
		Long: `List uploaded DARs on the targeted participant. Useful for
"what's deployed right now?" sanity checks during dev.

By default lists DARs (PackageService.ListDars). Pass --packages to
switch to the per-.dalf view (PackageService.ListPackages) — handy
for chasing down which dependency is missing.

Pass --vetting (requires --instance) to add a per-participant vetting
column probing all three participants (app-user / app-provider / sv),
so you can see where a package is vetted after a "dar remove" unvet —
the same vetting signal the Web UI DAR screen shows.

Exit codes:
  0  Listing returned (even if empty)
  1  Invalid arguments
  4  RPC failed`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			client, err := conn.connect(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar list: %s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer func() { _ = client.Close() }()

			if packages {
				resp, err := client.Package.ListPackages(cmd.Context(), &adminproto.ListPackagesRequest{
					Limit:      limit,
					FilterName: filterName,
				})
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar list (--packages): %s\n", err)
					return localnet.AsExitError(localnet.ExitRuntimeFailure)
				}
				if enrich {
					rows := enrichPackages(cmd.Context(), client, resp.GetPackageDescriptions())
					if format == "json" {
						return writeJSON(cmd.OutOrStdout(), rows)
					}
					printEnrichedPackages(cmd.OutOrStdout(), rows)
					return nil
				}
				if format == "json" {
					return writeJSON(cmd.OutOrStdout(), resp.GetPackageDescriptions())
				}
				printPackages(cmd.OutOrStdout(), resp.GetPackageDescriptions())
				return nil
			}

			resp, err := client.Package.ListDars(cmd.Context(), &adminproto.ListDarsRequest{
				Limit:      limit,
				FilterName: filterName,
			})
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar list: %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			// --vetting enrichment: probe every participant for each
			// DAR's vetting state. The proposal lists vetting as a
			// `dar list` column; "vetted on the participant I'm
			// listing" is tautological (ListDars only returns vetted
			// DARs there), so the useful signal is cross-participant —
			// shared with the Web UI via darops.ListVetting.
			if vetting {
				rows, verr := vettingRows(cmd.Context(), &conn, resp.GetDars())
				if verr != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar list --vetting: %s\n", verr)
					return localnet.AsExitError(localnet.ExitUserError)
				}
				if format == "json" {
					return writeJSON(cmd.OutOrStdout(), rows)
				}
				printVettingDars(cmd.OutOrStdout(), rows)
				return nil
			}
			if enrich {
				rows := enrichDars(cmd.Context(), client, resp.GetDars())
				if format == "json" {
					return writeJSON(cmd.OutOrStdout(), rows)
				}
				printEnrichedDars(cmd.OutOrStdout(), rows)
				return nil
			}
			if format == "json" {
				return writeJSON(cmd.OutOrStdout(), resp.GetDars())
			}
			printDars(cmd.OutOrStdout(), resp.GetDars())
			return nil
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	cmd.Flags().BoolVar(&packages, "packages", false, "List individual .dalf packages instead of DARs.")
	cmd.Flags().BoolVar(&enrich, "enrich", false,
		"Add Daml-LF version + module count to each row by calling GetPackageContents per package. Adds latency.")
	cmd.Flags().BoolVar(&vetting, "vetting", false,
		"Add a per-participant vetting column (probes sv, app-provider, app-user). Requires --instance; ignored with --packages.")
	cmd.Flags().StringVar(&filterName, "filter-name", "", "Server-side name substring filter.")
	cmd.Flags().Int32Var(&limit, "limit", 0, "Max rows to return (0 = server default).")
	return cmd
}

func printDars(out io.Writer, dars []*adminproto.DarDescription) {
	if len(dars) == 0 {
		_, _ = fmt.Fprintln(out, "No DARs uploaded.")
		return
	}
	_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %s\n", "MAIN PACKAGE ID", "NAME", "VERSION", "DESCRIPTION")
	for _, d := range dars {
		_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %s\n",
			truncate(d.GetMain(), 32),
			truncate(d.GetName(), 30),
			truncate(d.GetVersion(), 10),
			d.GetDescription())
	}
}

func printPackages(out io.Writer, pkgs []*adminproto.PackageDescription) {
	if len(pkgs) == 0 {
		_, _ = fmt.Fprintln(out, "No packages uploaded.")
		return
	}
	_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %-10s %s\n", "PACKAGE ID", "NAME", "VERSION", "SIZE", "UPLOADED")
	for _, p := range pkgs {
		_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %-10d %s\n",
			truncate(p.GetPackageId(), 32),
			truncate(p.GetName(), 30),
			truncate(p.GetVersion(), 10),
			p.GetSize(),
			p.GetUploadedAt().AsTime().Format("2006-01-02T15:04:05Z"))
	}
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- vetting enrichment (--vetting) ----------------------------------

// vettingDarRow is one DAR plus its per-participant vetting verdicts.
// JSON output reuses the shared types.DARVettingRow shape for the
// participants slice so the CLI and Web UI describe vetting state
// identically.
type vettingDarRow struct {
	MainPackageID string                `json:"main_package_id"`
	Name          string                `json:"name"`
	Version       string                `json:"version"`
	Description   string                `json:"description,omitempty"`
	Participants  []types.DARVettingRow `json:"participants"`
}

// vettingRows probes every participant of --instance for each DAR's
// vetting state, reusing the neutral darops.ListVetting the Web UI's
// vetting endpoint also calls. Requires --instance (cross-participant
// probing reads the registry); returns a clear error otherwise.
func vettingRows(ctx context.Context, conn *connectFlags, dars []*adminproto.DarDescription) ([]vettingDarRow, error) {
	state, err := conn.resolveState()
	if err != nil {
		return nil, err
	}
	rows := make([]vettingDarRow, 0, len(dars))
	for _, d := range dars {
		rows = append(rows, vettingDarRow{
			MainPackageID: d.GetMain(),
			Name:          d.GetName(),
			Version:       d.GetVersion(),
			Description:   d.GetDescription(),
			Participants:  darops.ListVetting(ctx, darops.DialAdmin, state, d.GetMain()),
		})
	}
	return rows, nil
}

// vettingGlyph renders one participant's verdict for the text table:
// ✓ vetted, ✗ not vetted, ? un-probable (port/JWT/dial error).
func vettingGlyph(r types.DARVettingRow) string {
	switch {
	case r.Error != "":
		return "?"
	case r.Vetted:
		return "✓"
	default:
		return "✗"
	}
}

// printVettingDars renders the DAR list with a compact per-participant
// vetting column (e.g. "U:✓ P:✓ S:✗"), in the canonical
// app-user/app-provider/sv order darops.ListVetting returns.
func printVettingDars(out io.Writer, rows []vettingDarRow) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "No DARs uploaded.")
		return
	}
	_, _ = fmt.Fprintf(out, "%-32s %-26s %-10s %s\n",
		"MAIN PACKAGE ID", "NAME", "VERSION", "VETTING (user/provider/sv)")
	for _, r := range rows {
		_, _ = fmt.Fprintf(out, "%-32s %-26s %-10s %s\n",
			truncate(r.MainPackageID, 32),
			truncate(r.Name, 26),
			truncate(r.Version, 10),
			vettingSummary(r.Participants))
	}
	_, _ = fmt.Fprintln(out, "\nLegend: ✓ vetted · ✗ not vetted · ? unavailable (port/JWT/dial)")
}

// vettingSummary turns the per-participant rows into the compact
// "U:✓ P:✓ S:✗" column cell. Roles are abbreviated by first
// letter of the meaningful segment to keep the column narrow.
func vettingSummary(parts []types.DARVettingRow) string {
	abbr := map[string]string{
		"app-user":     "U",
		"app-provider": "P",
		"sv":           "S",
	}
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		label := abbr[p.Role]
		if label == "" {
			label = p.Role
		}
		cells = append(cells, label+":"+vettingGlyph(p))
	}
	return strings.Join(cells, " ")
}

// --- enrichment via GetPackageContents -------------------------------

// enrichedDarRow mirrors DarDescription + the extra columns the
// proposal asks for. Kept as a struct (rather than wrapping the proto)
// so JSON output is stable across upstream proto changes.
type enrichedDarRow struct {
	MainPackageID    string `json:"main_package_id"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	Description      string `json:"description"`
	LanguageVersion  string `json:"language_version,omitempty"`
	ModuleCount      int    `json:"module_count"`
	IsUtilityPackage bool   `json:"is_utility_package"`
	EnrichError      string `json:"enrich_error,omitempty"`
}

type enrichedPackageRow struct {
	PackageID        string `json:"package_id"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	Size             uint32 `json:"size_bytes"`
	UploadedAt       string `json:"uploaded_at"`
	LanguageVersion  string `json:"language_version,omitempty"`
	ModuleCount      int    `json:"module_count"`
	IsUtilityPackage bool   `json:"is_utility_package"`
	EnrichError      string `json:"enrich_error,omitempty"`
}

// enrichDars loops over each DAR description and fetches its main
// package's contents to surface LF version + module count. A failed
// per-row enrich is recorded as `enrich_error` rather than aborting
// the whole listing — proposal-level fields are best-effort.
func enrichDars(ctx context.Context, client *admin.Client, dars []*adminproto.DarDescription) []*enrichedDarRow {
	out := make([]*enrichedDarRow, 0, len(dars))
	for _, d := range dars {
		row := &enrichedDarRow{
			MainPackageID: d.GetMain(),
			Name:          d.GetName(),
			Version:       d.GetVersion(),
			Description:   d.GetDescription(),
		}
		contents, err := client.Package.GetPackageContents(ctx, &adminproto.GetPackageContentsRequest{
			PackageId: d.GetMain(),
		})
		if err != nil {
			row.EnrichError = err.Error()
		} else {
			row.LanguageVersion = contents.GetLanguageVersion()
			row.ModuleCount = len(contents.GetModules())
			row.IsUtilityPackage = contents.GetIsUtilityPackage()
		}
		out = append(out, row)
	}
	return out
}

func enrichPackages(ctx context.Context, client *admin.Client, pkgs []*adminproto.PackageDescription) []*enrichedPackageRow {
	out := make([]*enrichedPackageRow, 0, len(pkgs))
	for _, p := range pkgs {
		row := &enrichedPackageRow{
			PackageID:  p.GetPackageId(),
			Name:       p.GetName(),
			Version:    p.GetVersion(),
			Size:       p.GetSize(),
			UploadedAt: p.GetUploadedAt().AsTime().Format("2006-01-02T15:04:05Z"),
		}
		contents, err := client.Package.GetPackageContents(ctx, &adminproto.GetPackageContentsRequest{
			PackageId: p.GetPackageId(),
		})
		if err != nil {
			row.EnrichError = err.Error()
		} else {
			row.LanguageVersion = contents.GetLanguageVersion()
			row.ModuleCount = len(contents.GetModules())
			row.IsUtilityPackage = contents.GetIsUtilityPackage()
		}
		out = append(out, row)
	}
	return out
}

func printEnrichedDars(out io.Writer, rows []*enrichedDarRow) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "No DARs uploaded.")
		return
	}
	_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %-5s %-7s %s\n",
		"MAIN PACKAGE ID", "NAME", "VERSION", "LF", "MODULES", "DESCRIPTION")
	for _, r := range rows {
		_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %-5s %-7d %s\n",
			truncate(r.MainPackageID, 32),
			truncate(r.Name, 30),
			truncate(r.Version, 10),
			orDashS(r.LanguageVersion),
			r.ModuleCount,
			r.Description)
	}
}

func printEnrichedPackages(out io.Writer, rows []*enrichedPackageRow) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "No packages uploaded.")
		return
	}
	_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %-5s %-7s %-10s %s\n",
		"PACKAGE ID", "NAME", "VERSION", "LF", "MODULES", "SIZE", "UPLOADED")
	for _, r := range rows {
		_, _ = fmt.Fprintf(out, "%-32s %-30s %-10s %-5s %-7d %-10d %s\n",
			truncate(r.PackageID, 32),
			truncate(r.Name, 30),
			truncate(r.Version, 10),
			orDashS(r.LanguageVersion),
			r.ModuleCount,
			r.Size,
			r.UploadedAt)
	}
}
