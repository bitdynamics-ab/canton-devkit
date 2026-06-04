package token

import (
	"context"
	"errors"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestRunPartyNew_RejectsInvalidAlias — validation happens before any
// ledger dial, so this needs no live participant.
func TestRunPartyNew_RejectsInvalidAlias(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	for _, bad := range []string{"", "1bob", "-bob", "bob::x", "Bob"} {
		_, err := RunPartyNew(context.Background(), PartyOptions{Instance: "demo", Alias: bad})
		if !errors.Is(err, ErrAliasInvalid) {
			t.Errorf("alias %q: want ErrAliasInvalid, got %v", bad, err)
		}
	}
}

// TestRunPartyNew_RejectsDuplicateAlias — a name already in the registry
// is rejected before any allocation.
func TestRunPartyNew_RejectsDuplicateAlias(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	s, _ := registry.Read("demo")
	s.Parties = map[string]registry.PartyRef{"bob": {Alias: "bob", PartyID: "bob::1"}}
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	_, err := RunPartyNew(context.Background(), PartyOptions{Instance: "demo", Alias: "bob"})
	if !errors.Is(err, ErrAliasInUse) {
		t.Errorf("want ErrAliasInUse, got %v", err)
	}
}

// TestRunPartyList_ReturnsRegisteredSorted — list reads state and sorts
// by alias (no endpoint → no seeding).
func TestRunPartyList_ReturnsRegisteredSorted(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	s, _ := registry.Read("demo")
	s.Parties = map[string]registry.PartyRef{
		"carol": {Alias: "carol", PartyID: "carol::3"},
		"alice": {Alias: "alice", PartyID: "alice::1"},
	}
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	got, err := RunPartyList(context.Background(), PartyOptions{Instance: "demo"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Alias != "alice" || got[1].Alias != "carol" {
		t.Errorf("not sorted by alias: %+v", got)
	}
}

// TestRunPartyRemove — forgets an alias; unknown alias errors.
func TestRunPartyRemove(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	s, _ := registry.Read("demo")
	s.Parties = map[string]registry.PartyRef{"bob": {Alias: "bob", PartyID: "bob::1"}}
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	if err := RunPartyRemove("demo", "bob"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	s2, _ := registry.Read("demo")
	if _, ok := s2.Parties["bob"]; ok {
		t.Error("alias bob still present after remove")
	}
	if err := RunPartyRemove("demo", "ghost"); !errors.Is(err, ErrAliasUnknown) {
		t.Errorf("want ErrAliasUnknown, got %v", err)
	}
}
