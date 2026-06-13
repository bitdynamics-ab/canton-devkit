package ledger

import (
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// Shared per-event projection for transaction updates.
//
// Both the CLI Explorer (`contracts watch`, `tx ls`,
// internal/cli/localnet/contracts.go) and the Web UI transactions
// handler (internal/ui/handlers/transactions.go projectUpdate) need
// to walk a transaction's events and pull out the create/archive/
// exercise discriminator, contract id, template id and witnesses.
// Previously the UI projected this correctly while the CLI threw it
// away (it printed only the event COUNT), so `contracts watch` was
// useless as the "live tail of create/archive events" the proposal
// promised. Extracting the projection here gives both surfaces one
// decoder so they can never drift on which events they surface.

// EventKind is the create/archive/exercise discriminator of a
// projected ledger event. Stable string values — they are emitted in
// the CLI's `--format json` and the Web UI's JSON, so renaming one is
// a wire break.
type EventKind string

const (
	EventCreated   EventKind = "created"
	EventArchived  EventKind = "archived"
	EventExercised EventKind = "exercised"
)

// EventSummary is the flattened view of one CreatedEvent /
// ArchivedEvent / ExercisedEvent. Fields not applicable to a kind are
// left zero (e.g. Choice/Consuming are exercise-only; Signatories is
// create-only).
type EventSummary struct {
	Kind       EventKind
	ContractID string
	TemplateID string
	Witnesses  []string

	// Created-only.
	Signatories []string
	Observers   []string

	// Exercised-only.
	Choice        string
	ActingParties []string
	Consuming     bool
}

// ProjectTransactionEvents flattens a transaction's events into
// EventSummary rows in their original order. Events the proto carries
// that aren't one of created/archived/exercised are skipped. A nil
// transaction yields a nil slice.
func ProjectTransactionEvents(tx *lapiv2.Transaction) []EventSummary {
	if tx == nil {
		return nil
	}
	evs := tx.GetEvents()
	out := make([]EventSummary, 0, len(evs))
	for _, e := range evs {
		if c := e.GetCreated(); c != nil {
			out = append(out, EventSummary{
				Kind:        EventCreated,
				ContractID:  c.GetContractId(),
				TemplateID:  identString(c.GetTemplateId()),
				Witnesses:   c.GetWitnessParties(),
				Signatories: c.GetSignatories(),
				Observers:   c.GetObservers(),
			})
			continue
		}
		if a := e.GetArchived(); a != nil {
			out = append(out, EventSummary{
				Kind:       EventArchived,
				ContractID: a.GetContractId(),
				TemplateID: identString(a.GetTemplateId()),
				Witnesses:  a.GetWitnessParties(),
			})
			continue
		}
		if x := e.GetExercised(); x != nil {
			out = append(out, EventSummary{
				Kind:          EventExercised,
				ContractID:    x.GetContractId(),
				TemplateID:    identString(x.GetTemplateId()),
				Witnesses:     x.GetWitnessParties(),
				Choice:        x.GetChoice(),
				ActingParties: x.GetActingParties(),
				Consuming:     x.GetConsuming(),
			})
		}
	}
	return out
}

// identString renders a proto Identifier as "package:Module:Entity"
// — the canonical form both surfaces show. Empty string for a nil id.
func identString(id *lapiv2.Identifier) string {
	if id == nil {
		return ""
	}
	return id.GetPackageId() + ":" + id.GetModuleName() + ":" + id.GetEntityName()
}
