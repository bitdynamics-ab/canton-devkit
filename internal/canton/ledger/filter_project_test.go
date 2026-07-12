package ledger

import (
	"testing"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// TestBuildEventFormat_Wildcard pins that the flag-less default emits
// a NON-NIL FiltersForAnyParty (the participant rejects an EventFormat
// with both filter maps empty). The shared builder is consumed by both
// the CLI and the Web UI Explorer, so this invariant guards both.
func TestBuildEventFormat_Wildcard(t *testing.T) {
	ef := BuildEventFormat(nil, nil, true)
	if ef.GetFiltersForAnyParty() == nil {
		t.Fatal("wildcard EventFormat must have non-nil FiltersForAnyParty")
	}
	if len(ef.GetFiltersByParty()) != 0 {
		t.Errorf("wildcard must not set FiltersByParty: %v", ef.GetFiltersByParty())
	}
	if !ef.GetVerbose() {
		t.Error("verbose flag dropped")
	}
}

// TestBuildEventFormat_ByPartyWithTemplate pins that a party set + a
// template selector builds a FiltersByParty entry per party, each
// carrying the template cumulative filter.
func TestBuildEventFormat_ByPartyWithTemplate(t *testing.T) {
	ef := BuildEventFormat([]string{"alice", "bob"}, []string{"Token:Holding"}, false)
	byParty := ef.GetFiltersByParty()
	if len(byParty) != 2 {
		t.Fatalf("want 2 party entries, got %d (%v)", len(byParty), byParty)
	}
	for _, p := range []string{"alice", "bob"} {
		f, ok := byParty[p]
		if !ok {
			t.Errorf("missing party %q", p)
			continue
		}
		cum := f.GetCumulative()
		if len(cum) != 1 || cum[0].GetTemplateFilter() == nil {
			t.Errorf("party %q has no template filter: %v", p, cum)
			continue
		}
		if got := cum[0].GetTemplateFilter().GetTemplateId().GetEntityName(); got != "Holding" {
			t.Errorf("party %q template entity = %q, want Holding", p, got)
		}
	}
}

// TestBuildTemplateFilters_Forms covers the accepted template syntaxes
// and the reject path.
func TestBuildTemplateFilters_Forms(t *testing.T) {
	// "Module:Entity" — no package pin.
	f, err := BuildTemplateFilters([]string{"Token:Holding"})
	if err != nil {
		t.Fatalf("Module:Entity: %v", err)
	}
	tid := f.GetCumulative()[0].GetTemplateFilter().GetTemplateId()
	if tid.GetPackageId() != "" || tid.GetModuleName() != "Token" || tid.GetEntityName() != "Holding" {
		t.Errorf("Module:Entity parsed wrong: %+v", tid)
	}
	// "pkg:Module:Entity" — a bare package name becomes a "#name"
	// LF-v2 package-name reference.
	f, err = BuildTemplateFilters([]string{"mypkg:Token:Holding"})
	if err != nil {
		t.Fatalf("pkg:Module:Entity: %v", err)
	}
	tid = f.GetCumulative()[0].GetTemplateFilter().GetTemplateId()
	if tid.GetPackageId() != "#mypkg" {
		t.Errorf("package id = %q, want #mypkg", tid.GetPackageId())
	}
	// nil/empty → nil, no error.
	if f, err := BuildTemplateFilters(nil); f != nil || err != nil {
		t.Errorf("empty template should yield (nil,nil), got (%v,%v)", f, err)
	}
	// A single-segment value is rejected.
	if _, err := BuildTemplateFilters([]string{"NoColon"}); err == nil {
		t.Error("single-segment template should be rejected")
	}
}

// TestBuildUpdateFormat_Shape pins that the TransactionShape selector
// is honoured — replay needs LEDGER_EFFECTS, the table needs ACS_DELTA.
func TestBuildUpdateFormat_Shape(t *testing.T) {
	uf := BuildUpdateFormat([]string{"alice"}, nil, true,
		lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS)
	got := uf.GetIncludeTransactions().GetTransactionShape()
	if got != lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS {
		t.Errorf("shape = %v, want LEDGER_EFFECTS", got)
	}
}

// TestProjectReplayEvents pins the replay-specific projection: it
// carries NodeID + the exercised choice detail that the flat
// ProjectTransactionEvents omits, so the per-party `tx replay` tree
// renders on both the CLI and Web UI from one decoder.
func TestProjectReplayEvents(t *testing.T) {
	tx := &lapiv2.Transaction{
		Events: []*lapiv2.Event{
			{Event: &lapiv2.Event_Created{Created: &lapiv2.CreatedEvent{
				NodeId:      0,
				ContractId:  "c-created",
				TemplateId:  &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
				Signatories: []string{"alice"},
				Observers:   []string{"bob"},
			}}},
			{Event: &lapiv2.Event_Exercised{Exercised: &lapiv2.ExercisedEvent{
				NodeId:        2,
				ContractId:    "c-exercised",
				TemplateId:    &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
				Choice:        "Transfer",
				Consuming:     true,
				ActingParties: []string{"alice"},
			}}},
		},
	}
	got := ProjectReplayEvents(tx)
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d", len(got))
	}
	if got[0].Kind != EventCreated || got[0].NodeID != 0 {
		t.Errorf("event 0 = %+v, want created node 0", got[0])
	}
	if got[1].Kind != EventExercised || got[1].NodeID != 2 {
		t.Errorf("event 1 = %+v, want exercised node 2", got[1])
	}
	if got[1].Choice != "Transfer" || !got[1].Consuming {
		t.Errorf("event 1 choice/consuming = %q/%v", got[1].Choice, got[1].Consuming)
	}
	if got := ProjectReplayEvents(nil); got != nil {
		t.Errorf("nil tx should yield nil, got %v", got)
	}
}

// TestRecordToMap pins the shared payload decoder both surfaces use so
// `contracts ls --format json` and the UI drawer render field values
// identically.
func TestRecordToMap(t *testing.T) {
	rec := &lapiv2.Record{
		Fields: []*lapiv2.RecordField{
			{Label: "owner", Value: &lapiv2.Value{Sum: &lapiv2.Value_Party{Party: "alice"}}},
			{Label: "amount", Value: &lapiv2.Value{Sum: &lapiv2.Value_Numeric{Numeric: "42.5"}}},
			{Label: "active", Value: &lapiv2.Value{Sum: &lapiv2.Value_Bool{Bool: true}}},
			{Label: "tags", Value: &lapiv2.Value{Sum: &lapiv2.Value_List{List: &lapiv2.List{
				Elements: []*lapiv2.Value{
					{Sum: &lapiv2.Value_Text{Text: "x"}},
					{Sum: &lapiv2.Value_Text{Text: "y"}},
				},
			}}}},
		},
	}
	m := RecordToMap(rec)
	if m["owner"] != "alice" {
		t.Errorf("owner = %v, want alice", m["owner"])
	}
	if m["amount"] != "42.5" {
		t.Errorf("amount = %v, want 42.5", m["amount"])
	}
	if m["active"] != true {
		t.Errorf("active = %v, want true", m["active"])
	}
	tags, ok := m["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "x" {
		t.Errorf("tags = %v, want [x y]", m["tags"])
	}
	if RecordToMap(nil) != nil {
		t.Error("nil record should yield nil map")
	}
}
