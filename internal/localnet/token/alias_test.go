package token

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func sampleParties() map[string]registry.PartyRef {
	return map[string]registry.PartyRef{
		"bob":   {Alias: "bob", PartyID: "bob::1220fa", Role: "app-user", IsLocal: true},
		"alice": {Alias: "alice", PartyID: "alice::1220ab", Role: "app-provider", IsLocal: true},
	}
}

func TestResolveAlias(t *testing.T) {
	p := sampleParties()
	if got := ResolveAlias(p, "bob"); got != "bob::1220fa" {
		t.Errorf("alias bob: got %q", got)
	}
	// Unknown name passes through (already an id, or ledger rejects it).
	if got := ResolveAlias(p, "carol::99"); got != "carol::99" {
		t.Errorf("passthrough: got %q", got)
	}
	if got := ResolveAlias(p, ""); got != "" {
		t.Errorf("empty: got %q", got)
	}
	if got := ResolveAlias(nil, "bob"); got != "bob" {
		t.Errorf("nil map passthrough: got %q", got)
	}
}

func TestAliasFor(t *testing.T) {
	p := sampleParties()
	if got := AliasFor(p, "alice::1220ab"); got != "alice" {
		t.Errorf("reverse: got %q", got)
	}
	if got := AliasFor(p, "zzz::00"); got != "" {
		t.Errorf("unknown id: got %q, want empty", got)
	}
}

func TestShortLabel(t *testing.T) {
	p := sampleParties()
	// Registered → alias.
	if got := ShortLabel(p, "bob::1220fa"); got != "bob" {
		t.Errorf("registered: got %q", got)
	}
	// Unregistered → prefix before ::.
	if got := ShortLabel(p, "carol::deadbeef"); got != "carol" {
		t.Errorf("prefix: got %q", got)
	}
	// No "::" → unchanged.
	if got := ShortLabel(p, "plain"); got != "plain" {
		t.Errorf("plain: got %q", got)
	}
}
