package ledger

import (
	"testing"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// TestProjectTransactionEvents pins the shared create/archive/exercise
// projection both the CLI Explorer and the Web UI transactions handler
// consume. The bug it guards: the CLI used to discard every per-event
// detail (it printed only the event COUNT), so `contracts watch` was
// useless as a live create/archive tail.
func TestProjectTransactionEvents(t *testing.T) {
	tx := &lapiv2.Transaction{
		Events: []*lapiv2.Event{
			{Event: &lapiv2.Event_Created{Created: &lapiv2.CreatedEvent{
				ContractId:     "cid-create",
				TemplateId:     &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
				WitnessParties: []string{"alice"},
				Signatories:    []string{"alice"},
				Observers:      []string{"bob"},
			}}},
			{Event: &lapiv2.Event_Exercised{Exercised: &lapiv2.ExercisedEvent{
				ContractId:     "cid-exercise",
				TemplateId:     &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
				Choice:         "Transfer",
				ActingParties:  []string{"alice"},
				Consuming:      true,
				WitnessParties: []string{"alice", "bob"},
			}}},
			{Event: &lapiv2.Event_Archived{Archived: &lapiv2.ArchivedEvent{
				ContractId:     "cid-archive",
				TemplateId:     &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
				WitnessParties: []string{"alice"},
			}}},
		},
	}

	got := ProjectTransactionEvents(tx)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3 (%+v)", len(got), got)
	}

	if got[0].Kind != EventCreated {
		t.Errorf("event 0 kind = %q, want created", got[0].Kind)
	}
	if got[0].ContractID != "cid-create" {
		t.Errorf("event 0 contract id = %q", got[0].ContractID)
	}
	if got[0].TemplateID != "pkg:Token:Holding" {
		t.Errorf("event 0 template = %q, want pkg:Token:Holding", got[0].TemplateID)
	}
	if len(got[0].Signatories) != 1 || got[0].Signatories[0] != "alice" {
		t.Errorf("event 0 signatories = %v", got[0].Signatories)
	}

	if got[1].Kind != EventExercised {
		t.Errorf("event 1 kind = %q, want exercised", got[1].Kind)
	}
	if got[1].Choice != "Transfer" || !got[1].Consuming {
		t.Errorf("event 1 choice/consuming = %q/%v", got[1].Choice, got[1].Consuming)
	}

	if got[2].Kind != EventArchived {
		t.Errorf("event 2 kind = %q, want archived", got[2].Kind)
	}
	if got[2].ContractID != "cid-archive" {
		t.Errorf("event 2 contract id = %q", got[2].ContractID)
	}
}

func TestProjectTransactionEvents_NilSafe(t *testing.T) {
	if got := ProjectTransactionEvents(nil); got != nil {
		t.Errorf("nil transaction should yield nil, got %v", got)
	}
	if got := ProjectTransactionEvents(&lapiv2.Transaction{}); len(got) != 0 {
		t.Errorf("empty transaction should yield no events, got %v", got)
	}
}
