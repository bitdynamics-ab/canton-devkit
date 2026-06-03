package dar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	cdkdar "github.com/bitdynamics-ab/canton-devkit/internal/dar"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

// buildInfo wires `dar info`. The command auto-routes on the single
// positional argument:
//
//   - if the arg looks like a 64-hex package ID, we query the
//     participant's Admin API (PackageService.GetPackageContents +
//     GetPackageReferences) to surface module names, language version,
//     and which DARs reference the package.
//   - otherwise, the arg is treated as a path to a local .dar file
//     and we use the offline parser (`internal/dar`).
//
// This matches the proposal's signature `dar info <package-id|package-name>`
// while keeping the offline file path useful for CI / on-disk
// validation flows.
//
// Deep template/choice/field/interface inspection (modules → choices
// with field types) is still future work (BIT-115) — both code paths
// surface what their underlying API actually returns today.
func buildInfo() *cobra.Command {
	var (
		format  string
		verbose bool
		conn    connectFlags
	)
	cmd := &cobra.Command{
		Use:   "info <dar-path|package-id>",
		Short: "Inspect a local DAR file or an uploaded package on a participant",
		Long: `Inspect a Daml package by package id (queries a participant)
or by local DAR file path (parses bytes on disk).

Routing:
  dar info /path/to/foo.dar          → offline file parser
  dar info <64-hex package id>       → participant query via Admin API
                                       (PackageService.GetPackageContents +
                                       PackageService.GetPackageReferences)

Offline path output:
  - SDK version that built the DAR
  - main package: name, version, package ID, Daml-LF version
  - dependency packages: name, version, ID, LF version, file size
  - SHA256 of the DAR for tamper checks

Participant-query path output:
  - module list (names only — Canton's Admin API doesn't yet emit
    templates/choices/fields)
  - language version (Daml-LF) and is_utility_package flag
  - DARs that reference the package (from GetPackageReferences)

Deep template/choice/field/interface inspection from local DARs is
tracked in BIT-115; that work will plug into the offline path here.

Exit codes:
  0  Success
  1  Invalid arguments / file unreadable
  4  Parse failure or RPC failure`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			arg := args[0]

			// Heuristic: if it's a known existing file on disk OR
			// doesn't match the 64-hex package-id shape, treat as file.
			// Otherwise treat as a package id and query the participant.
			if _, err := os.Stat(arg); err == nil {
				return runInfoFile(cmd.OutOrStdout(), cmd.ErrOrStderr(), arg, format, verbose)
			}
			if !looksLikePackageID(arg) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"dar info: %q is neither an existing file nor a 64-hex package id\n", arg)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return runInfoParticipant(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				arg, format, verbose, &conn)
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false,
		"Offline path: show every dependency. Participant path: include every referenced DAR.")
	conn.register(cmd)
	return cmd
}

// looksLikePackageID returns true when s is exactly 64 hexadecimal
// characters — Canton package ids are SHA256 hex of the ArchivePayload.
// Anything else (filenames, partial ids, names) routes to the
// offline-file branch.
func looksLikePackageID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// --- offline file path -----------------------------------------------

func runInfoFile(out, errw io.Writer, path, format string, verbose bool) error {
	info, err := cdkdar.Open(path)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "dar info: %s\n", err)
		return localnet.AsExitError(localnet.ExitRuntimeFailure)
	}
	if format == "json" {
		printInfoJSON(out, info)
	} else {
		printInfoText(out, info, verbose)
	}
	return nil
}

func printInfoJSON(out io.Writer, info *cdkdar.Info) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(info)
}

func printInfoText(out io.Writer, info *cdkdar.Info, verbose bool) {
	main := info.MainPackage()

	_, _ = fmt.Fprintf(out, "DAR: %s\n", info.Path)
	if info.Manifest != nil {
		_, _ = fmt.Fprintf(out, "  SDK version:   %s\n", info.Manifest.SdkVersion)
	}
	_, _ = fmt.Fprintf(out, "  File SHA256:   %s\n", info.SHA256)
	_, _ = fmt.Fprintln(out)

	if main != nil {
		_, _ = fmt.Fprintln(out, "Main package:")
		_, _ = fmt.Fprintf(out, "  Name:          %s\n", main.Name)
		if main.Version != "" {
			_, _ = fmt.Fprintf(out, "  Version:       %s\n", main.Version)
		}
		_, _ = fmt.Fprintf(out, "  Package ID:    %s\n", main.PackageID)
		_, _ = fmt.Fprintf(out, "  Daml-LF:       %s\n", main.LFVersion())
		_, _ = fmt.Fprintf(out, "  Size:          %d bytes\n", main.SizeBytes)
		_, _ = fmt.Fprintln(out)
	}

	deps := make([]*cdkdar.PackageMeta, 0, len(info.Packages))
	for _, p := range info.Packages {
		if !p.IsMain {
			deps = append(deps, p)
		}
	}
	_, _ = fmt.Fprintf(out, "Dependencies: %d package(s)\n", len(deps))

	if verbose {
		_, _ = fmt.Fprintf(out, "  %-50s %-12s %-7s %-10s %s\n", "NAME", "VERSION", "LF", "SIZE", "PACKAGE ID")
		for _, p := range deps {
			ver := p.Version
			if ver == "" {
				ver = "-"
			}
			_, _ = fmt.Fprintf(out, "  %-50s %-12s %-7s %-10d %s\n",
				truncate(p.Name, 50), truncate(ver, 12), p.LFVersion(), p.SizeBytes, p.PackageID)
		}
	} else if len(deps) > 0 {
		lfCounts := map[string]int{}
		for _, p := range deps {
			lfCounts[p.LFVersion()]++
		}
		_, _ = fmt.Fprintf(out, "  LF versions:  ")
		first := true
		for lf, n := range lfCounts {
			if !first {
				_, _ = fmt.Fprint(out, ", ")
			}
			first = false
			_, _ = fmt.Fprintf(out, "%s (%d)", lf, n)
		}
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "  Use --verbose for the full dependency list.\n")
	}
}

// --- participant-query path ------------------------------------------

func runInfoParticipant(
	ctx context.Context,
	out, errw io.Writer,
	pkgID, format string,
	verbose bool,
	conn *connectFlags,
) error {
	client, err := conn.connect(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "dar info: %s\n", err)
		return localnet.AsExitError(localnet.ExitUserError)
	}
	defer func() { _ = client.Close() }()

	contents, err := client.Package.GetPackageContents(ctx, &adminproto.GetPackageContentsRequest{
		PackageId: pkgID,
	})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "dar info: GetPackageContents: %s\n", err)
		return localnet.AsExitError(localnet.ExitRuntimeFailure)
	}
	refs, err := client.Package.GetPackageReferences(ctx, &adminproto.GetPackageReferencesRequest{
		PackageId: pkgID,
	})
	if err != nil {
		// Soft-fail: the references query is informational; if it
		// errors (older participant, RBAC, etc.) we still surface the
		// module list rather than abort.
		_, _ = fmt.Fprintf(errw, "Warning: GetPackageReferences failed: %s\n", err)
	}

	if format == "json" {
		printParticipantInfoJSON(out, pkgID, contents, refs)
	} else {
		printParticipantInfoText(out, pkgID, contents, refs, verbose)
	}
	return nil
}

type participantInfoView struct {
	PackageID        string                         `json:"package_id"`
	Description      *adminproto.PackageDescription `json:"description"`
	LanguageVersion  string                         `json:"language_version"`
	IsUtilityPackage bool                           `json:"is_utility_package"`
	ModuleCount      int                            `json:"module_count"`
	Modules          []string                       `json:"modules"`
	ReferencedByDars []*adminproto.DarDescription   `json:"referenced_by_dars,omitempty"`
}

func toView(pkgID string, contents *adminproto.GetPackageContentsResponse, refs *adminproto.GetPackageReferencesResponse) *participantInfoView {
	v := &participantInfoView{
		PackageID:        pkgID,
		LanguageVersion:  contents.GetLanguageVersion(),
		IsUtilityPackage: contents.GetIsUtilityPackage(),
		Description:      contents.GetDescription(),
		ModuleCount:      len(contents.GetModules()),
	}
	for _, m := range contents.GetModules() {
		v.Modules = append(v.Modules, m.GetName())
	}
	if refs != nil {
		v.ReferencedByDars = refs.GetDars()
	}
	return v
}

func printParticipantInfoJSON(out io.Writer, pkgID string, contents *adminproto.GetPackageContentsResponse, refs *adminproto.GetPackageReferencesResponse) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(toView(pkgID, contents, refs))
}

func printParticipantInfoText(out io.Writer, pkgID string, contents *adminproto.GetPackageContentsResponse, refs *adminproto.GetPackageReferencesResponse, verbose bool) {
	v := toView(pkgID, contents, refs)
	_, _ = fmt.Fprintf(out, "Package: %s\n", v.PackageID)
	if v.Description != nil {
		_, _ = fmt.Fprintf(out, "  Name:             %s\n", v.Description.GetName())
		if ver := v.Description.GetVersion(); ver != "" {
			_, _ = fmt.Fprintf(out, "  Version:          %s\n", ver)
		}
		if size := v.Description.GetSize(); size > 0 {
			_, _ = fmt.Fprintf(out, "  Size:             %d bytes\n", size)
		}
		if t := v.Description.GetUploadedAt(); t != nil {
			_, _ = fmt.Fprintf(out, "  Uploaded at:      %s\n", t.AsTime().Format("2006-01-02T15:04:05Z"))
		}
	}
	_, _ = fmt.Fprintf(out, "  Daml-LF version:  %s\n", orDashS(v.LanguageVersion))
	_, _ = fmt.Fprintf(out, "  Utility package:  %t\n", v.IsUtilityPackage)
	_, _ = fmt.Fprintf(out, "  Modules:          %d\n", v.ModuleCount)

	if v.ModuleCount > 0 {
		if verbose || v.ModuleCount <= 10 {
			for _, m := range v.Modules {
				_, _ = fmt.Fprintf(out, "    - %s\n", m)
			}
		} else {
			for _, m := range v.Modules[:5] {
				_, _ = fmt.Fprintf(out, "    - %s\n", m)
			}
			_, _ = fmt.Fprintf(out, "    … and %d more (use --verbose to list)\n", v.ModuleCount-5)
		}
	}

	if len(v.ReferencedByDars) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Referenced by %d DAR(s):\n", len(v.ReferencedByDars))
		for _, d := range v.ReferencedByDars {
			_, _ = fmt.Fprintf(out, "  - %s (%s %s)\n", d.GetMain(), d.GetName(), d.GetVersion())
		}
	}
}

// orDashS returns "-" for empty strings; mirrors diff.go's orDash so
// the two output paths render placeholders identically.
func orDashS(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// truncate returns s clipped to width with an ellipsis when shortened.
// Used by the offline-path text renderer (dependency table) and by
// list.go's row printer.
func truncate(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 1 {
		return s[:width]
	}
	return s[:width-1] + "…"
}
