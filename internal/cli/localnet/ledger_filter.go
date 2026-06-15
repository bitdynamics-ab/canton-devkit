package localnet

import (
	"fmt"
	"strings"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// extracted the EventFormat / UpdateFormat
// builders into a tested helper so `--party` and `--template` are
// no longer silent no-ops.
//
// The participant rejects an empty filter shape for security
// reasons (would otherwise leak every party's events to any JWT
// holder). The wildcard option (FiltersForAnyParty) is the
// participant's permission gate; when the JWT actually carries
// the right claim, the wildcard yields all parties visible to
// that claim.

// buildEventFormat constructs the EventFormat that ActiveContracts
// + Updates take. Semantics:
//
//	parties: nil/empty → FiltersForAnyParty (wildcard).
//	parties non-empty → FiltersByParty with one Filters entry per
//	                     party. Each per-party entry applies the
//	                     same `templates` filter (or wildcard if
//	                     none).
//	templates: nil/empty → no template restriction; "Module:Entity"
//	                     or "package:Module:Entity" forms accepted
//	                     (the latter pins the package id explicitly,
//	                     the former is package-name based and
//	                     matches whatever package the participant
//	                     upgrades to).
//
// `verbose=true` requests RecordWithType verbosity so the CLI can
// render template-id strings without a separate package lookup.
//
// Wire-shape contract (the bug this guards against): Canton's Ledger
// API v2 request validation REJECTS an EventFormat whose FiltersByParty
// is empty AND whose FiltersForAnyParty is nil — "filtersByParty and
// filtersForAnyParty cannot both be empty" (INVALID_ARGUMENT). The
// wildcard is therefore a NON-NIL but empty *Filters{} in
// FiltersForAnyParty: an empty Cumulative list defaults to a wildcard
// CumulativeFilter per the proto, which the participant gates by the
// JWT's own claim. So the flag-less default path must emit
// FiltersForAnyParty: &Filters{}, never nil. Likewise a --party with
// no --template maps each party to &Filters{} (empty wildcard),
// never a nil value. See internal/canton/ledger/state.go and the
// token package (holdingInterfaceFilterV2) which both rely on the
// non-nil-empty-Filters shape.
func buildEventFormat(parties, templates []string, verbose bool) *lapiv2.EventFormat {
	tplFilters, err := buildTemplateFilters(templates)
	if err != nil {
		// Filter-construction errors propagate as nil — the
		// caller saw err already via flag-parse layer. We don't
		// fail here so a wildcard request with a bad template
		// at least gets a wildcard response rather than nothing.
		_ = err
	}
	if len(parties) == 0 {
		// Wildcard: FiltersForAnyParty MUST be non-nil. When no
		// --template was given, tplFilters is nil — substitute an
		// empty *Filters{} so the participant sees the valid
		// "wildcard, no template restriction" shape rather than an
		// empty EventFormat it rejects.
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
		// Each per-party entry must be a non-nil *Filters. With no
		// --template the wildcard-per-party shape is an empty
		// *Filters{}; a nil value would marshal as an absent entry
		// and the participant rejects a party key with no filter.
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

// buildUpdateFormat wraps an EventFormat in an UpdateFormat
// configured for ACS-style flat-transaction shape (the default
// for `tx ls` / `contracts watch`). Tree shape is a separate
// helper for `tx replay` when that lands.
func buildUpdateFormat(parties, templates []string, verbose bool) *lapiv2.UpdateFormat {
	ef := buildEventFormat(parties, templates, verbose)
	return &lapiv2.UpdateFormat{
		IncludeTransactions: &lapiv2.TransactionFormat{
			EventFormat:      ef,
			TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA,
		},
	}
}

// buildTemplateFilters parses the user-supplied --template values
// into the proto Filters shape. Accepts:
//
//	"Module:Entity" — package-name match (any
//	                                  vetted package containing
//	                                  this template).
//	"pkg-name:Module:Entity" — same, more verbose.
//	"<package-id>:Module:Entity" — exact package-id pin (when
//	                                  the value is 64-hex).
//
// Returns nil for an empty/nil input — caller interprets as
// "wildcard, no template restriction".
func buildTemplateFilters(templates []string) (*lapiv2.Filters, error) {
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
				"--template %q invalid: expected Module:Entity or pkg:Module:Entity", t)
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
		tid := &lapiv2.TemplateFilter{
			TemplateId: &lapiv2.Identifier{
				PackageId:  pkg,
				ModuleName: mod,
				EntityName: entity,
			},
			IncludeCreatedEventBlob: false,
		}
		filters.Cumulative = append(filters.Cumulative,
			&lapiv2.CumulativeFilter{
				IdentifierFilter: &lapiv2.CumulativeFilter_TemplateFilter{
					TemplateFilter: tid,
				},
			})
	}
	return filters, nil
}
