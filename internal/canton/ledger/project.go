package ledger

import (
	"encoding/json"

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

// ReplayEvent is the flattened view of one event in a REPLAYED
// transaction (TRANSACTION_SHAPE_LEDGER_EFFECTS). Unlike EventSummary
// — which projects the flat ACS-delta shape `tx ls` / `contracts
// watch` use — replay carries the NodeID and the exercised-choice
// detail so the per-party visibility projection (the proposal's `tx
// replay`) renders the full tree. Both the CLI `tx replay` and the
// Web UI replay endpoint build their wire rows from this so they can
// never drift on which fields a replay surfaces.
type ReplayEvent struct {
	Kind          EventKind
	NodeID        int32
	ContractID    string
	TemplateID    string
	Choice        string   // exercised only
	ActingParties []string // exercised only
	Consuming     bool     // exercised only
	Signatories   []string // created only
	Observers     []string // created only
}

// ProjectReplayEvents flattens a replayed transaction's events into
// ReplayEvent rows in their original (tree-walk) order. A nil
// transaction yields a nil slice.
func ProjectReplayEvents(tx *lapiv2.Transaction) []ReplayEvent {
	if tx == nil {
		return nil
	}
	evs := tx.GetEvents()
	out := make([]ReplayEvent, 0, len(evs))
	for _, e := range evs {
		switch {
		case e.GetCreated() != nil:
			ce := e.GetCreated()
			out = append(out, ReplayEvent{
				Kind:        EventCreated,
				NodeID:      ce.GetNodeId(),
				ContractID:  ce.GetContractId(),
				TemplateID:  identString(ce.GetTemplateId()),
				Signatories: ce.GetSignatories(),
				Observers:   ce.GetObservers(),
			})
		case e.GetExercised() != nil:
			xe := e.GetExercised()
			out = append(out, ReplayEvent{
				Kind:          EventExercised,
				NodeID:        xe.GetNodeId(),
				ContractID:    xe.GetContractId(),
				TemplateID:    identString(xe.GetTemplateId()),
				Choice:        xe.GetChoice(),
				ActingParties: xe.GetActingParties(),
				Consuming:     xe.GetConsuming(),
			})
		case e.GetArchived() != nil:
			ae := e.GetArchived()
			out = append(out, ReplayEvent{
				Kind:       EventArchived,
				NodeID:     ae.GetNodeId(),
				ContractID: ae.GetContractId(),
				TemplateID: identString(ae.GetTemplateId()),
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

// RecordToMap converts a Daml-LF Record proto into a JSON-friendly
// map[string]any, recursing into nested records and lists. Shared by
// the Web UI Explorer (contract drawer + ACS payload preview) and the
// CLI `contracts ls --format json` so both decode payloads
// identically. Best-effort: value kinds the decoder hasn't
// enumerated fall back to their textual proto form rather than
// panicking. Returns nil for a nil record.
func RecordToMap(r *lapiv2.Record) map[string]any {
	if r == nil {
		return nil
	}
	out := make(map[string]any, len(r.Fields))
	for _, f := range r.Fields {
		out[f.GetLabel()] = ValueToAny(f.GetValue())
	}
	return out
}

// ValueToAny converts a single Daml-LF Value proto into a
// JSON-friendly Go value. See [RecordToMap].
func ValueToAny(v *lapiv2.Value) any {
	if v == nil {
		return nil
	}
	switch s := v.Sum.(type) {
	case *lapiv2.Value_Bool:
		return s.Bool
	case *lapiv2.Value_Int64:
		return s.Int64
	case *lapiv2.Value_Numeric:
		return s.Numeric
	case *lapiv2.Value_Text:
		return s.Text
	case *lapiv2.Value_Party:
		return s.Party
	case *lapiv2.Value_ContractId:
		return s.ContractId
	case *lapiv2.Value_Date:
		return s.Date
	case *lapiv2.Value_Timestamp:
		return s.Timestamp
	case *lapiv2.Value_Record:
		return RecordToMap(s.Record)
	case *lapiv2.Value_List:
		items := s.List.GetElements()
		out := make([]any, len(items))
		for i, e := range items {
			out[i] = ValueToAny(e)
		}
		return out
	case *lapiv2.Value_Optional:
		opt := s.Optional.GetValue()
		if opt == nil {
			return nil
		}
		return ValueToAny(opt)
	default:
		// Variants, enums, maps, GenMap — proto-textual fallback.
		b, _ := json.Marshal(v.Sum)
		return string(b)
	}
}
