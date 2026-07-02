package localnet

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
)

// txnUpdate builds a GetUpdatesResponse carrying one transaction with
// a single create event, for the render tests.
func txnUpdate(updateID string, offset int64, contractID string) *lapiv2.GetUpdatesResponse {
	return &lapiv2.GetUpdatesResponse{
		Update: &lapiv2.GetUpdatesResponse_Transaction{
			Transaction: &lapiv2.Transaction{
				UpdateId:    updateID,
				Offset:      offset,
				EffectiveAt: timestamppb.Now(),
				Events: []*lapiv2.Event{
					{Event: &lapiv2.Event_Created{Created: &lapiv2.CreatedEvent{
						ContractId: contractID,
						TemplateId: &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
					}}},
				},
			},
		},
	}
}

// feedUpdates returns a closed channel pre-loaded with the given
// updates — the same StreamItem shape client.Updates yields.
func feedUpdates(updates ...*lapiv2.GetUpdatesResponse) <-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse] {
	ch := make(chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], len(updates))
	for _, u := range updates {
		ch <- ledger.StreamItem[*lapiv2.GetUpdatesResponse]{Value: u}
	}
	close(ch)
	return ch
}

// TestRenderUpdateStream_PrintsPerEventDetail pins that
// `contracts watch` must print the create/archive events
// it streams (kind, template, contract id), not just the event COUNT.
func TestRenderUpdateStream_PrintsPerEventDetail(t *testing.T) {
	var out bytes.Buffer
	stream := feedUpdates(txnUpdate("tx-1", 120, "00deadbeefcontractid"))

	if err := renderUpdateStream(&out, "dev", nil, "text", stream, 0); err != nil {
		t.Fatalf("renderUpdateStream: %v", err)
	}
	got := out.String()
	// The transaction header is still there.
	if !strings.Contains(got, "offset=120") {
		t.Errorf("missing transaction header; got:\n%s", got)
	}
	// Each event must get its own line — kind + template must show.
	if !strings.Contains(got, "created") {
		t.Errorf("missing create-event line; got:\n%s", got)
	}
	if !strings.Contains(got, "Token:Holding") {
		t.Errorf("missing template id on event line; got:\n%s", got)
	}
}

// TestRenderUpdateStream_JSONIncludesEvents pins that --format json
// carries the projected events.
func TestRenderUpdateStream_JSONIncludesEvents(t *testing.T) {
	var out bytes.Buffer
	stream := feedUpdates(txnUpdate("tx-1", 7, "cid-1"))

	if err := renderUpdateStream(&out, "dev", nil, "json", stream, 0); err != nil {
		t.Fatalf("renderUpdateStream: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid NDJSON line: %v\n%s", err, out.String())
	}
	events, ok := got["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("events = %v, want 1 entry", got["events"])
	}
	ev := events[0].(map[string]any)
	if ev["kind"] != "created" {
		t.Errorf("event kind = %v, want created", ev["kind"])
	}
}

// TestRenderUpdateList_NewestFirstAndScannedWindow pins that
// `tx ls` must return the NEWEST `limit` rows (the
// stream is ascending, so a naive first-N would return the oldest),
// and it must print the scanned offset window so a filtered query that
// finds nothing isn't mistaken for "no such transactions".
func TestRenderUpdateList_NewestFirstAndScannedWindow(t *testing.T) {
	var out bytes.Buffer
	// Five transactions arrive oldest→newest (offsets 10..50).
	stream := feedUpdates(
		txnUpdate("tx-10", 10, "c10"),
		txnUpdate("tx-20", 20, "c20"),
		txnUpdate("tx-30", 30, "c30"),
		txnUpdate("tx-40", 40, "c40"),
		txnUpdate("tx-50", 50, "c50"),
	)
	// limit=2 → keep the NEWEST two (40, 50), printed newest-first.
	if err := renderUpdateList(&out, "dev", nil, "text", stream, 2, 5, 50); err != nil {
		t.Fatalf("renderUpdateList: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "offset=50") || !strings.Contains(got, "offset=40") {
		t.Errorf("expected the newest two (40,50); got:\n%s", got)
	}
	if strings.Contains(got, "offset=10") || strings.Contains(got, "offset=30") {
		t.Errorf("did not expect the older rows; got:\n%s", got)
	}
	// Newest-first ordering: offset=50 appears before offset=40.
	if strings.Index(got, "offset=50") > strings.Index(got, "offset=40") {
		t.Errorf("rows not newest-first; got:\n%s", got)
	}
	// Scanned window footer is present.
	if !strings.Contains(got, "scanned offsets (5, 50]") {
		t.Errorf("missing scanned-window footer; got:\n%s", got)
	}
}

// TestRenderUpdateList_EmptyShowsScannedWindow ensures a filtered query
// that finds nothing tells the user WHAT was scanned, instead of a
// bare "no transactions" that hides whether matches sit just outside.
func TestRenderUpdateList_EmptyShowsScannedWindow(t *testing.T) {
	var out bytes.Buffer
	stream := feedUpdates() // nothing matched
	if err := renderUpdateList(&out, "dev", nil, "text", stream, 50, 100, 200); err != nil {
		t.Fatalf("renderUpdateList: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "no matching transactions in scanned offsets (100, 200]") {
		t.Errorf("empty result should report the scanned window; got:\n%s", got)
	}
}

// TestRenderUpdateList_JSONShape pins the scanned_from/scanned_to keys
// the JSON consumer relies on to know the window.
func TestRenderUpdateList_JSONShape(t *testing.T) {
	var out bytes.Buffer
	stream := feedUpdates(txnUpdate("tx-1", 50, "c1"))
	if err := renderUpdateList(&out, "dev", nil, "json", stream, 10, 5, 50); err != nil {
		t.Fatalf("renderUpdateList: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if got["scanned_from"].(float64) != 5 || got["scanned_to"].(float64) != 50 {
		t.Errorf("scanned window = %v..%v, want 5..50", got["scanned_from"], got["scanned_to"])
	}
	txns, ok := got["transactions"].([]any)
	if !ok || len(txns) != 1 {
		t.Fatalf("transactions = %v, want 1", got["transactions"])
	}
}
