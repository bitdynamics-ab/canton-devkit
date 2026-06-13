package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Explorer happy-path coverage for the Web UI transactions handler
// (finding #28: the only existing tests stopped at the pre-gRPC
// validation branches). We run a real in-process gRPC participant on
// a loopback TCP port — the handler dials `localhost:<port>` exactly
// like production — so the newest-N ring buffer, the bounded scan
// window, and the create/archive event projection (the subjects of
// finding #22 / #26) are exercised end to end without a live Canton.

// fakeParticipant implements just enough of the Ledger API v2 surface
// for the transactions handler: GetLedgerEnd + GetUpdates
// (StateService is unrelated to updates, but the handler's LedgerEnd
// probe lives there) and ListUserRights (UserManagementService) so
// ResolveActAndReadParties returns a party set.
type fakeParticipant struct {
	lapiv2.UnimplementedStateServiceServer
	lapiv2.UnimplementedUpdateServiceServer
	adminv2.UnimplementedUserManagementServiceServer

	ledgerEnd int64
	parties   []string
	// updates are sent in order on GetUpdates. They should be
	// ascending-offset to mimic the real participant.
	updates []*lapiv2.GetUpdatesResponse
}

func (f *fakeParticipant) GetLedgerEnd(_ context.Context, _ *lapiv2.GetLedgerEndRequest) (*lapiv2.GetLedgerEndResponse, error) {
	return &lapiv2.GetLedgerEndResponse{Offset: f.ledgerEnd}, nil
}

func (f *fakeParticipant) ListUserRights(_ context.Context, _ *adminv2.ListUserRightsRequest) (*adminv2.ListUserRightsResponse, error) {
	rights := make([]*adminv2.Right, 0, len(f.parties))
	for _, p := range f.parties {
		rights = append(rights, &adminv2.Right{
			Kind: &adminv2.Right_CanActAs_{CanActAs: &adminv2.Right_CanActAs{Party: p}},
		})
	}
	return &adminv2.ListUserRightsResponse{Rights: rights}, nil
}

func (f *fakeParticipant) GetUpdates(_ *lapiv2.GetUpdatesRequest, stream grpc.ServerStreamingServer[lapiv2.GetUpdatesResponse]) error {
	for _, u := range f.updates {
		if err := stream.Send(u); err != nil {
			return err
		}
	}
	return nil
}

// startFakeParticipant boots the gRPC server on a loopback port and
// returns the port. The server is torn down on test cleanup.
func startFakeParticipant(t *testing.T, fake *fakeParticipant) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	lapiv2.RegisterStateServiceServer(srv, fake)
	lapiv2.RegisterUpdateServiceServer(srv, fake)
	adminv2.RegisterUserManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port
}

// seedInstanceWithCreds writes a registry state.json with the given
// participant ledger port AND an app-user JWT, so the transactions
// handler can dial the fake participant.
func seedInstanceWithCreds(t *testing.T, name string, ledgerPort int) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Ports = map[string]int{"participant_ledger_app-user": ledgerPort}
	s.Credentials = map[string]registry.Credential{
		"app-user": {Role: "app-user", User: "ledger-api-user", JWT: "test-jwt"},
	}
	s.Status = registry.StatusRunning
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

func txUpdate(offset int64, updateID, contractID string) *lapiv2.GetUpdatesResponse {
	return &lapiv2.GetUpdatesResponse{
		Update: &lapiv2.GetUpdatesResponse_Transaction{
			Transaction: &lapiv2.Transaction{
				UpdateId:   updateID,
				Offset:     offset,
				RecordTime: timestamppb.New(time.Unix(offset, 0)),
				Events: []*lapiv2.Event{
					{Event: &lapiv2.Event_Created{Created: &lapiv2.CreatedEvent{
						ContractId:     contractID,
						TemplateId:     &lapiv2.Identifier{PackageId: "pkg", ModuleName: "Token", EntityName: "Holding"},
						WitnessParties: []string{"alice"},
					}}},
				},
			},
		},
	}
}

// TestTransactions_HappyPath_NewestFirst drives the full handler
// against the fake participant and asserts the response shape, the
// newest-first ordering, the create-event projection, and the new
// scanned_from / window_truncated fields.
func TestTransactions_HappyPath_NewestFirst(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	fake := &fakeParticipant{
		ledgerEnd: 50,
		parties:   []string{"alice"},
		updates: []*lapiv2.GetUpdatesResponse{
			txUpdate(10, "tx-10", "c10"),
			txUpdate(20, "tx-20", "c20"),
			txUpdate(30, "tx-30", "c30"),
		},
	}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions?role=app-user")
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

	if got["ledger_end"].(float64) != 50 {
		t.Errorf("ledger_end = %v, want 50", got["ledger_end"])
	}
	// scanned_from must be a recent floor, NOT 0 (finding #22). With
	// end=50 < window span, it clamps to 0 here — but the key must be
	// present so the client knows the window.
	if _, ok := got["scanned_from"]; !ok {
		t.Error("response missing scanned_from")
	}
	if got["window_truncated"] != false {
		t.Errorf("window_truncated = %v, want false (small history drains to EOF)", got["window_truncated"])
	}

	txns, ok := got["transactions"].([]any)
	if !ok || len(txns) != 3 {
		t.Fatalf("transactions = %v, want 3", got["transactions"])
	}
	// Newest-first: offset 30 leads.
	first := txns[0].(map[string]any)
	if first["offset"].(float64) != 30 {
		t.Errorf("first row offset = %v, want 30 (newest first)", first["offset"])
	}
	last := txns[2].(map[string]any)
	if last["offset"].(float64) != 10 {
		t.Errorf("last row offset = %v, want 10", last["offset"])
	}
	// The create event must be projected (kind/contract_id/template) —
	// not just an event count.
	events, ok := first["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("first row events = %v, want 1", first["events"])
	}
	ev := events[0].(map[string]any)
	if ev["kind"] != "create" {
		t.Errorf("event kind = %v, want create", ev["kind"])
	}
	if ev["contract_id"] != "c30" {
		t.Errorf("event contract_id = %v, want c30", ev["contract_id"])
	}
	if ev["template"] != "pkg:Token:Holding" {
		t.Errorf("event template = %v, want pkg:Token:Holding", ev["template"])
	}
}

// TestTransactions_HappyPath_NewestNWhenOverLimit pins the newest-N
// ring semantics (the core of finding #22): when the participant has
// more transactions than `limit`, the handler returns the NEWEST
// `limit`, not the oldest — even though the stream arrives ascending.
func TestTransactions_HappyPath_NewestNWhenOverLimit(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	updates := make([]*lapiv2.GetUpdatesResponse, 0, 10)
	for i := int64(1); i <= 10; i++ {
		updates = append(updates, txUpdate(i, fmt.Sprintf("tx-%d", i), fmt.Sprintf("c%d", i)))
	}
	fake := &fakeParticipant{ledgerEnd: 10, parties: []string{"alice"}, updates: updates}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions?role=app-user&limit=3")
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
	txns := got["transactions"].([]any)
	if len(txns) != 3 {
		t.Fatalf("got %d rows, want 3 (limit)", len(txns))
	}
	// Newest three are offsets 10, 9, 8 — NOT 1, 2, 3.
	wantOffsets := []float64{10, 9, 8}
	for i, w := range wantOffsets {
		off := txns[i].(map[string]any)["offset"].(float64)
		if off != w {
			t.Errorf("row %d offset = %v, want %v (newest-N, not oldest-N)", i, off, w)
		}
	}
}

// TestTransactions_WindowFlooredAtRecentOffset is the direct
// regression for finding #22: on a long-lived ledger the scan must
// start at a RECENT offset (end - window span), not 0. Before the
// fix, BeginExclusive was hard-coded to 0, so a busy LocalNet would
// process only the oldest streamCap updates and label ancient
// activity "newest first". Here end is past the window span, so
// scanned_from must be end-span, not 0.
func TestTransactions_WindowFlooredAtRecentOffset(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	const end = transactionsWindowSpan + 100
	fake := &fakeParticipant{
		ledgerEnd: end,
		parties:   []string{"alice"},
		updates:   []*lapiv2.GetUpdatesResponse{txUpdate(end-1, "tx-recent", "c-recent")},
	}
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sf := got["scanned_from"].(float64); sf != float64(end-transactionsWindowSpan) {
		t.Errorf("scanned_from = %v, want %d (recent floor, NOT 0)", sf, end-transactionsWindowSpan)
	}
}

// TestTransactions_HappyPath_NoPartyRights pins the EXPLORER_NEEDS_PARTY_JWT
// path: when the JWT resolves to zero parties, the handler returns a
// structured 503 (not a 502) so the frontend renders the friendly
// empty state.
func TestTransactions_HappyPath_NoPartyRights(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	fake := &fakeParticipant{ledgerEnd: 5, parties: nil} // no party rights
	port := startFakeParticipant(t, fake)
	seedInstanceWithCreds(t, "dev", port)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/dev/transactions?role=app-user")
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
