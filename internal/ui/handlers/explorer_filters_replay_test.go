package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Coverage for the new Explorer parity surfaces:
//   - GET .../transactions party/template/from/to filters
//   - GET .../transactions/{update_id}/replay per-party projection
//
// Both run against the in-process fakeParticipant from
// explorer_happy_test.go so the filter the handler builds and the
// replay projection are exercised end to end without a live Canton.

// TestTransactions_FilterParamsBuildByPartyFilter pins that an
// explicit ?party / ?template builds a FiltersByParty EventFormat with
// the requested parties + a template cumulative filter — the parity
// the CLI's `tx ls --party/--template` already had. With an
// explicit party the handler must NOT consult the JWT's party rights
// (so the fake's empty rights don't matter).
func TestTransactions_FilterParamsBuildByPartyFilter(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{
		ledgerEnd: 50,
		parties:   nil, // JWT has NO rights — explicit ?party must bypass this
		updates:   []*lapiv2.GetUpdatesResponse{txUpdate(20, "tx-20", "c20")},
	}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL +
		"/api/instances/dev/transactions?role=app-user&party=alice,bob&template=Token:Holding")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (explicit party should bypass JWT rights)", resp.StatusCode)
	}

	ef := fake.lastUpdatesReq.GetUpdateFormat().GetIncludeTransactions().GetEventFormat()
	if ef == nil {
		t.Fatal("no EventFormat built")
	}
	byParty := ef.GetFiltersByParty()
	if _, ok := byParty["alice"]; !ok {
		t.Errorf("FiltersByParty missing alice: %v", byParty)
	}
	if _, ok := byParty["bob"]; !ok {
		t.Errorf("FiltersByParty missing bob: %v", byParty)
	}
	// The template filter must be attached to each party entry.
	cum := byParty["alice"].GetCumulative()
	if len(cum) == 0 || cum[0].GetTemplateFilter() == nil {
		t.Errorf("alice filter has no template cumulative filter: %v", cum)
	}
	if got := cum[0].GetTemplateFilter().GetTemplateId().GetEntityName(); got != "Holding" {
		t.Errorf("template entity = %q, want Holding", got)
	}
}

// TestTransactions_TemplateOnlyResolvesParties pins the fix for the
// user-id-token wart: a ?template with NO ?party must resolve the JWT's
// own parties and build a FiltersByParty EventFormat (the template
// attached per party), NOT a bare FiltersForAnyParty wildcard — which a
// Splice user-id JWT would be PermissionDenied on. Also pins that the
// bare package name is emitted as an LF-v2 "#name" reference. Mirrors
// the CLI's resolveDefaultParties + BuildTemplateFilters, keeping the
// two surfaces in parity.
func TestTransactions_TemplateOnlyResolvesParties(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{
		ledgerEnd: 50,
		parties:   []string{"alice"}, // JWT resolves to alice
		updates:   []*lapiv2.GetUpdatesResponse{txUpdate(20, "tx-20", "c20")},
	}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL +
		"/api/instances/dev/transactions?role=app-user&template=splice-amulet:Splice.Amulet:Amulet")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (template-only must resolve JWT parties)", resp.StatusCode)
	}

	ef := fake.lastUpdatesReq.GetUpdateFormat().GetIncludeTransactions().GetEventFormat()
	if ef == nil {
		t.Fatal("no EventFormat built")
	}
	if ef.GetFiltersForAnyParty() != nil {
		t.Errorf("template-only must NOT use the any-party wildcard: %v", ef.GetFiltersForAnyParty())
	}
	f, ok := ef.GetFiltersByParty()["alice"]
	if !ok {
		t.Fatalf("FiltersByParty missing resolved party alice: %v", ef.GetFiltersByParty())
	}
	cum := f.GetCumulative()
	if len(cum) == 0 || cum[0].GetTemplateFilter() == nil {
		t.Fatalf("alice filter has no template cumulative filter: %v", cum)
	}
	if got := cum[0].GetTemplateFilter().GetTemplateId().GetPackageId(); got != "#splice-amulet" {
		t.Errorf("PackageId = %q, want #splice-amulet (LF-v2 package-name reference)", got)
	}
}

// TestTransactions_FromToWindow pins that ?from/?to set the exact
// scanned offset window (mirrors the CLI's --from/--to), overriding
// the default recent window.
func TestTransactions_FromToWindow(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{
		ledgerEnd: 9000,
		parties:   []string{"alice"},
		updates:   []*lapiv2.GetUpdatesResponse{txUpdate(150, "tx-150", "c150")},
	}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL +
		"/api/instances/dev/transactions?role=app-user&from=100&to=200")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sf := got["scanned_from"].(float64); sf != 100 {
		t.Errorf("scanned_from = %v, want 100 (explicit --from)", sf)
	}
	// The gRPC request must carry the exact window.
	if be := fake.lastUpdatesReq.GetBeginExclusive(); be != 100 {
		t.Errorf("BeginExclusive = %d, want 100", be)
	}
	if ei := fake.lastUpdatesReq.GetEndInclusive(); ei != 200 {
		t.Errorf("EndInclusive = %d, want 200", ei)
	}
}

// TestTransactions_BadFromTo400 pins that a non-integer ?from fails
// 400 before any gRPC work (input validation parity with the CLI).
func TestTransactions_BadFromTo400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstanceWithCreds(t, "dev", 1) // port need not be live; we 400 first
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions?from=notanumber")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestTransactions_FromGreaterThanTo400 pins the begin>end guard.
func TestTransactions_FromGreaterThanTo400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{ledgerEnd: 9000, parties: []string{"alice"}}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions?from=500&to=100")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (from must be <= to)", resp.StatusCode)
	}
}

// replayUpdate builds a GetUpdateResponse carrying one transaction
// with a create + a consuming exercise, for the replay handler.
func replayUpdate(updateID string, offset int64) *lapiv2.GetUpdateResponse {
	return &lapiv2.GetUpdateResponse{
		Update: &lapiv2.GetUpdateResponse_Transaction{
			Transaction: &lapiv2.Transaction{
				UpdateId:    updateID,
				Offset:      offset,
				WorkflowId:  "wf-1",
				EffectiveAt: timestamppb.New(time.Unix(offset, 0)),
				Events: []*lapiv2.Event{
					{Event: &lapiv2.Event_Created{Created: &lapiv2.CreatedEvent{
						NodeId:      0,
						ContractId:  "c-created",
						TemplateId:  &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
						Signatories: []string{"alice"},
					}}},
					{Event: &lapiv2.Event_Exercised{Exercised: &lapiv2.ExercisedEvent{
						NodeId:        1,
						ContractId:    "c-exercised",
						TemplateId:    &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
						Choice:        "Transfer",
						Consuming:     true,
						ActingParties: []string{"alice"},
					}}},
				},
			},
		},
	}
}

// TestTxReplay_HappyPath drives the replay handler against the fake
// and asserts the per-event projection (kind/node_id/choice), the
// LEDGER_EFFECTS shape, and that an explicit ?party threads into the
// EventFormat.
func TestTxReplay_HappyPath(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{
		ledgerEnd: 50,
		parties:   nil, // explicit ?party must bypass JWT rights
		byID:      map[string]*lapiv2.GetUpdateResponse{"tx-42": replayUpdate("tx-42", 42)},
	}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL +
		"/api/instances/dev/transactions/tx-42/replay?role=app-user&party=alice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["update_id"] != "tx-42" {
		t.Errorf("update_id = %v, want tx-42", got["update_id"])
	}
	if got["offset"].(float64) != 42 {
		t.Errorf("offset = %v, want 42", got["offset"])
	}
	events, ok := got["events"].([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("events = %v, want 2", got["events"])
	}
	created := events[0].(map[string]any)
	if created["kind"] != "created" {
		t.Errorf("event[0] kind = %v, want created", created["kind"])
	}
	exercised := events[1].(map[string]any)
	if exercised["kind"] != "exercised" {
		t.Errorf("event[1] kind = %v, want exercised", exercised["kind"])
	}
	if exercised["choice"] != "Transfer" {
		t.Errorf("exercised choice = %v, want Transfer", exercised["choice"])
	}
	if exercised["consuming"] != true {
		t.Errorf("exercised consuming = %v, want true", exercised["consuming"])
	}
	if exercised["node_id"].(float64) != 1 {
		t.Errorf("exercised node_id = %v, want 1", exercised["node_id"])
	}

	// The replay must request the LEDGER_EFFECTS shape, not ACS_DELTA.
	shape := fake.lastUpdateByID.GetUpdateFormat().GetIncludeTransactions().GetTransactionShape()
	if shape != lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS {
		t.Errorf("replay shape = %v, want LEDGER_EFFECTS", shape)
	}
	// Explicit ?party must thread through to the EventFormat.
	byParty := fake.lastUpdateByID.GetUpdateFormat().GetIncludeTransactions().GetEventFormat().GetFiltersByParty()
	if _, ok := byParty["alice"]; !ok {
		t.Errorf("replay EventFormat missing alice: %v", byParty)
	}
}

// TestTxReplay_NotFound pins that an unknown update id surfaces a 404
// (the participant's NotFound mapped to "not visible"), not a 502.
func TestTxReplay_NotFound(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{ledgerEnd: 50, parties: []string{"alice"}, byID: map[string]*lapiv2.GetUpdateResponse{}}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions/missing/replay?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if got := toStr(body["code"]); got != ErrCodeNotFound {
		t.Errorf("code = %q, want %s", got, ErrCodeNotFound)
	}
}

// TestTxReplay_NoPartyRights pins the EXPLORER_NEEDS_PARTY_JWT path
// when no ?party is given and the JWT resolves to zero parties.
func TestTxReplay_NoPartyRights(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{ledgerEnd: 50, parties: nil} // no rights, no ?party
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions/tx-1/replay?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if got := toStr(body["code"]); got != "EXPLORER_NEEDS_PARTY_JWT" {
		t.Errorf("code = %q, want EXPLORER_NEEDS_PARTY_JWT", got)
	}
}
