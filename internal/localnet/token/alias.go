package token

import (
	"sort"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// Party aliases let a developer say `--to bob` instead of
// pasting a 130-char `bob::1220fa…` party id. The alias → party-id map
// lives in registry.State.Parties; these helpers resolve in both
// directions and are pure (they take the map) so they unit-test without
// a ledger or a registry file.

// ResolveAlias maps a name to a party id. If name is a registered alias,
// its party id is returned; otherwise name is passed through unchanged
// (it's already a party id, or an unknown token the ledger will reject).
// Matching is case-sensitive on the alias key.
func ResolveAlias(parties map[string]registry.PartyRef, name string) string {
	if name == "" {
		return ""
	}
	if ref, ok := parties[name]; ok && ref.PartyID != "" {
		return ref.PartyID
	}
	return name
}

// AliasFor reverse-maps a party id to its registered alias, or "" when
// no alias points at it. If several aliases map to the same id (unusual)
// the lexicographically-first is returned for determinism.
func AliasFor(parties map[string]registry.PartyRef, partyID string) string {
	if partyID == "" {
		return ""
	}
	matches := make([]string, 0, 1)
	for alias, ref := range parties {
		if ref.PartyID == partyID {
			matches = append(matches, alias)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[0]
}

// ShortLabel returns the best human label for a party id: its registered
// alias if one exists, else the readable prefix before "::" (e.g.
// `bob::1220fa…` → `bob`). Used by CLI tables and as the display name the
// API attaches to party ids.
func ShortLabel(parties map[string]registry.PartyRef, partyID string) string {
	if a := AliasFor(parties, partyID); a != "" {
		return a
	}
	if i := strings.Index(partyID, "::"); i > 0 {
		return partyID[:i]
	}
	return partyID
}

// partiesFromState reads an instance's registered party ids (the values
// of the alias map). Returns nil for an unknown instance or an empty
// registry — callers treat nil as "no extra parties to widen to".
func partiesFromState(instance string) []string {
	if instance == "" {
		return nil
	}
	state, err := registry.Read(instance)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(state.Parties))
	for _, ref := range state.Parties {
		if ref.PartyID != "" {
			out = append(out, ref.PartyID)
		}
	}
	sort.Strings(out)
	return out
}

// aliasMapForInstance loads the alias registry for an instance, or an
// empty map when the instance is unknown / has none. Convenience for the
// resolve/display paths that only have an instance name in hand.
func aliasMapForInstance(instance string) map[string]registry.PartyRef {
	if instance == "" {
		return map[string]registry.PartyRef{}
	}
	state, err := registry.Read(instance)
	if err != nil || state.Parties == nil {
		return map[string]registry.PartyRef{}
	}
	return state.Parties
}
