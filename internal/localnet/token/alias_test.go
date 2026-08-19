package token

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func seedPartyInstance(t *testing.T, name string) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState(name, "0.6.12")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = registry.StatusRunning
	s.Parties = map[string]registry.PartyRef{
		"alice": {Alias: "alice", PartyID: "alice::1220ab", Role: "app-provider", IsLocal: true},
		"bob":   {Alias: "bob", PartyID: "bob::1220fa", Role: "app-user", IsLocal: true},
	}
	s.Ports = map[string]int{
		"participant_ledger_app-provider": 3901,
		"participant_ledger_app-user":     2901,
	}
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
}

func TestReceiverRole_KnownParty(t *testing.T) {
	seedPartyInstance(t, "rr-test")
	if got := receiverRole("rr-test", "bob::1220fa"); got != "app-user" {
		t.Errorf("bob: got %q, want app-user", got)
	}
	if got := receiverRole("rr-test", "alice::1220ab"); got != "app-provider" {
		t.Errorf("alice: got %q, want app-provider", got)
	}
}

func TestReceiverRole_UnknownParty(t *testing.T) {
	seedPartyInstance(t, "rr-test2")
	if got := receiverRole("rr-test2", "carol::9999"); got != "" {
		t.Errorf("unknown: got %q, want empty", got)
	}
}

func TestReceiverRole_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	if got := receiverRole("no-such-instance", "bob::1220fa"); got != "" {
		t.Errorf("unknown instance: got %q, want empty", got)
	}
}

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
