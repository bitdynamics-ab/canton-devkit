package ledger

import (
	"fmt"
	"strings"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// Shared party + template filter construction for the Explorer
// surfaces.
//
// Both the CLI (`contracts ls`, `contracts watch`, `tx ls`) and the
// Web UI handlers (contracts.go, transactions.go) need to translate a
// party set + a list of "Module:Entity" / "pkg:Module:Entity"
// template strings into the proto EventFormat / UpdateFormat shapes.
// Hosting it here lets the CLI and the UI share one decoder so
// `--party`/`--template` behave identically and can never drift,
// instead of each surface hand-rolling its own FiltersByParty map.

// BuildTemplateFilters parses user-supplied template selectors into
// the proto Filters shape. Accepts:
//
//	"Module:Entity"               — no package selector; PackageId is
//	                                left empty. A Daml-LF-v2 participant
//	                                (Splice 0.6.x) REJECTS an empty
//	                                package id — give a package name to
//	                                get a filter that resolves.
//	"pkg-name:Module:Entity"      — package-NAME reference. Emitted in
//	                                the LF-v2 "#pkg-name" form so the
//	                                participant resolves it to the highest
//	                                vetted version of that package.
//	"#pkg-name:Module:Entity"     — same, already in the "#name" form;
//	                                passed through unchanged.
//	"<package-id>:Module:Entity"  — exact pin on a concrete 64-hex
//	                                Daml-LF package-id.
//
// The package selector is normalised by [packageRef]: a bare name gains
// the LF-v2 "#" prefix; a "#name" or a 64-hex package-id passes through.
//
// Returns nil for an empty/nil input — the caller interprets that as
// "wildcard, no template restriction".
func BuildTemplateFilters(templates []string) (*lapiv2.Filters, error) {
	if len(templates) == 0 {
		return nil, nil
	}
	filters := &lapiv2.Filters{}
	for _, t := range templates {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		parts := strings.Split(t, ":")
		if len(parts) < 2 {
			return nil, fmt.Errorf(
				"--template %q invalid: expected \"#pkg-name:Module:Entity\" or \"Module:Entity\"", t)
		}
		var pkg, mod, entity string
		switch len(parts) {
		case 2:
			// "Module:Entity" — no package pin.
			mod, entity = parts[0], parts[1]
		case 3:
			pkg, mod, entity = parts[0], parts[1], parts[2]
		default:
			return nil, fmt.Errorf(
				"--template %q has too many colon-separated parts", t)
		}
		filters.Cumulative = append(filters.Cumulative,
			&lapiv2.CumulativeFilter{
				IdentifierFilter: &lapiv2.CumulativeFilter_TemplateFilter{
					TemplateFilter: &lapiv2.TemplateFilter{
						TemplateId: &lapiv2.Identifier{
							PackageId:  packageRef(pkg),
							ModuleName: mod,
							EntityName: entity,
						},
						IncludeCreatedEventBlob: false,
					},
				},
			})
	}
	return filters, nil
}

// packageRef normalises the package selector from a "pkg:Module:Entity"
// template into the value Canton's LF-v2 Ledger API expects in
// Identifier.PackageId. A concrete 64-hex package-id is an exact pin and
// passes through untouched; any other non-empty value is a package NAME,
// which LF-v2 references with a leading "#" (a value already
// "#"-prefixed is passed through as-is). An empty selector — the bare
// "Module:Entity" form — stays empty; there is no name to reference.
func packageRef(pkg string) string {
	if pkg == "" || strings.HasPrefix(pkg, "#") || isHexPackageID(pkg) {
		return pkg
	}
	return "#" + pkg
}

// isHexPackageID reports whether s is a concrete Daml-LF package-id: the
// 64-char hex SHA-256 of the package. Package names never take this
// shape, so a match means the caller pinned an exact package id rather
// than a name to resolve.
func isHexPackageID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// BuildEventFormat constructs the EventFormat that ActiveContracts +
// Updates take. Semantics:
//
//	parties nil/empty → FiltersForAnyParty wildcard.
//	parties non-empty → FiltersByParty with one entry per party, each
//	                    applying the same template filter (or a
//	                    per-party wildcard when none).
//
// Wire-shape contract (the bug this guards against): Canton's Ledger
// API v2 request validation REJECTS an EventFormat whose
// FiltersByParty is empty AND whose FiltersForAnyParty is nil
// ("filtersByParty and filtersForAnyParty cannot both be empty"). The
// wildcard is therefore a NON-NIL but empty *Filters{}: an empty
// Cumulative list defaults to a wildcard CumulativeFilter the
// participant gates by the JWT's own claim. So the flag-less default
// path emits FiltersForAnyParty: &Filters{}, never nil; likewise a
// party with no template maps to &Filters{}, never a nil value.
//
// A template-parse error is swallowed (returns the wildcard) because
// callers validate `templates` separately at the input layer and we
// prefer a wildcard response over silently returning nothing. Use
// [BuildTemplateFilters] directly when you need the parse error.
func BuildEventFormat(parties, templates []string, verbose bool) *lapiv2.EventFormat {
	tplFilters, _ := BuildTemplateFilters(templates)
	if len(parties) == 0 {
		if tplFilters == nil {
			tplFilters = &lapiv2.Filters{}
		}
		return &lapiv2.EventFormat{
			FiltersForAnyParty: tplFilters,
			Verbose:            verbose,
		}
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		if tplFilters != nil {
			byParty[p] = tplFilters
		} else {
			byParty[p] = &lapiv2.Filters{}
		}
	}
	return &lapiv2.EventFormat{
		FiltersByParty: byParty,
		Verbose:        verbose,
	}
}

// BuildUpdateFormat wraps an EventFormat in an UpdateFormat with the
// given TransactionShape. Pass TRANSACTION_SHAPE_ACS_DELTA for the
// flat create/archive projection (`tx ls` / `contracts watch` / the
// UI transactions table) or TRANSACTION_SHAPE_LEDGER_EFFECTS for the
// hierarchical exercise tree (`tx replay` / the per-party projection).
func BuildUpdateFormat(parties, templates []string, verbose bool, shape lapiv2.TransactionShape) *lapiv2.UpdateFormat {
	return &lapiv2.UpdateFormat{
		IncludeTransactions: &lapiv2.TransactionFormat{
			EventFormat:      BuildEventFormat(parties, templates, verbose),
			TransactionShape: shape,
		},
	}
}
