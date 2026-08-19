package token

import (
	"context"
	"errors"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	regstate "github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// seedCrossParticipantInstance registers alice on app-provider and bob on
// app-user so receiverRole and ResolveLedgerEndpoint resolve correctly.
func seedCrossParticipantInstance(t *testing.T, name string) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := regstate.NewState(name, "0.6.12")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Status = regstate.StatusRunning
	s.Parties = map[string]regstate.PartyRef{
		"alice": {Alias: "alice", PartyID: "alice::1220ab", Role: "app-provider", IsLocal: true},
		"bob":   {Alias: "bob", PartyID: "bob::1220fa", Role: "app-user", IsLocal: true},
	}
	s.Ports = map[string]int{
		"participant_ledger_app-provider": 3901,
		"participant_ledger_app-user":     2901,
	}
	if err := regstate.Write(s); err != nil {
		t.Fatal(err)
	}
}

// newNilLedgerClient builds a *ledger.Client backed by a lazy gRPC connection
// to a placeholder endpoint. No real server is needed because grpc.NewClient
// is non-blocking; any RPC call against it will fail immediately, which is
// what we want (tests should never reach real RPCs).
func newNilLedgerClient(t *testing.T) *ledger.Client {
	t.Helper()
	c, err := ledger.Dial(context.Background(), ledger.DialOptions{
		Endpoint:  "localhost:1",
		PlainText: true,
		ExtraDialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		t.Fatalf("newNilLedgerClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// withStubbedSenderDial replaces dialSenderFn with one that returns a no-op
// *ledger.Client, allowing execution to reach the accept-side dial without a
// live participant. All subsequent RPCs against the no-op client fail with a
// gRPC connection-refused error (not our concern; we assert on the dial calls).
func withStubbedSenderDial(t *testing.T) {
	t.Helper()
	nopClient := newNilLedgerClient(t)
	prev := dialSenderFn
	dialSenderFn = func(ctx context.Context, conn LedgerConn) (*ledger.Client, func(), error) {
		return nopClient, func() {}, nil
	}
	t.Cleanup(func() { dialSenderFn = prev })
}

// withRecordingAcceptDial replaces dialLedgerConcreteFn (the accept-side
// seam) with a recorder. Returns the recorded connections and fails the dial
// so no real RPC is made against the receiver participant.
func withRecordingAcceptDial(t *testing.T) *[]LedgerConn {
	t.Helper()
	var dialed []LedgerConn
	prev := dialLedgerConcreteFn
	dialLedgerConcreteFn = func(ctx context.Context, conn LedgerConn) (*ledger.Client, func(), error) {
		dialed = append(dialed, conn)
		return nil, func() {}, errors.New("test: accept dial intercepted")
	}
	t.Cleanup(func() { dialLedgerConcreteFn = prev })
	return &dialed
}

// TestResolveAcceptConn_CrossParticipant verifies that resolveAcceptConn
// returns a conn pointing at the receiver's participant when the receiver
// lives on a different role than the sender.
func TestResolveAcceptConn_CrossParticipant(t *testing.T) {
	seedCrossParticipantInstance(t, "rac-test")

	senderConn := LedgerConn{
		Endpoint: "localhost:3901",
		Role:     "app-provider",
		Instance: "rac-test",
		Insecure: true,
	}
	got := resolveAcceptConn(senderConn, "rac-test", "bob::1220fa")
	if got.Role != "app-user" {
		t.Errorf("role = %q, want app-user", got.Role)
	}
	if got.Endpoint != "localhost:2901" {
		t.Errorf("endpoint = %q, want localhost:2901", got.Endpoint)
	}
}

// TestResolveAcceptConn_SameRole verifies that resolveAcceptConn returns the
// original conn unchanged when the receiver is on the same role as the sender.
func TestResolveAcceptConn_SameRole(t *testing.T) {
	seedCrossParticipantInstance(t, "rac-same")

	senderConn := LedgerConn{
		Endpoint: "localhost:3901",
		Role:     "app-provider",
		Instance: "rac-same",
		Insecure: true,
	}
	got := resolveAcceptConn(senderConn, "rac-same", "alice::1220ab")
	if got.Role != "app-provider" {
		t.Errorf("role = %q, want app-provider", got.Role)
	}
	if got.Endpoint != "localhost:3901" {
		t.Errorf("endpoint = %q, want localhost:3901 (unchanged)", got.Endpoint)
	}
}

// TestResolveAcceptConn_UnknownParty verifies that resolveAcceptConn falls
// back to the sender conn when the receiver party is not in the registry.
func TestResolveAcceptConn_UnknownParty(t *testing.T) {
	seedCrossParticipantInstance(t, "rac-unknown")

	senderConn := LedgerConn{
		Endpoint: "localhost:3901",
		Role:     "app-provider",
		Instance: "rac-unknown",
	}
	got := resolveAcceptConn(senderConn, "rac-unknown", "carol::9999")
	if got.Role != "app-provider" {
		t.Errorf("role = %q, want app-provider (fallback)", got.Role)
	}
}

// TestMintAcceptConn_CrossParticipant verifies that the accept-conn built
// inside runMintLive targets the receiver's participant when receiver is on
// a different role than the sender. The routing is delegated to
// resolveAcceptConn (separately tested); this test ensures runMintLive calls
// dialLedgerConcreteFn with the resolved conn by stubbing the sender dial so
// execution reaches the accept-dial site.
//
// The accept dial itself will fail (no real participant) — the assertion is on
// which conn was passed to dialLedgerConcreteFn, not on the call's outcome.
func TestMintAcceptConn_CrossParticipant(t *testing.T) {
	seedCrossParticipantInstance(t, "xp-mint")
	withStubbedSenderDial(t)
	// Also stub findTokenRulesDisclosed and mintViaOfferMint via the
	// instrument_v2 seams so execution reaches the accept dial.
	prevRules := findTokenRulesDisclosedFn
	findTokenRulesDisclosedFn = func(_ context.Context, _ *ledger.Client, _ string) (string, *lapiv2.DisclosedContract, error) {
		return "rules::cid", nil, nil
	}
	prevMint := mintViaOfferMintFn
	mintViaOfferMintFn = func(_ context.Context, _ *ledger.Client, _, _, _, _, _ string) (string, error) {
		return "offer::cid", nil
	}
	t.Cleanup(func() {
		findTokenRulesDisclosedFn = prevRules
		mintViaOfferMintFn = prevMint
	})
	acceptDialed := withRecordingAcceptDial(t)

	opts := MintOptions{
		Instance: "xp-mint",
		Role:     "app-provider",
		Endpoint: "localhost:3901",
		Insecure: true,
		To:       "bob::1220fa",
		Amount:   "100",
	}
	ref := regstate.TokenRef{IssuerParty: "alice::1220ab", Symbol: "T1"}
	_ = runMintLive(context.Background(), nil, opts, ref)

	if len(*acceptDialed) < 1 {
		t.Fatalf("accept dial not called; receiver participant was not dialed")
	}
	got := (*acceptDialed)[0]
	if got.Role != "app-user" {
		t.Errorf("accept dial role = %q, want app-user", got.Role)
	}
	if got.Endpoint != "localhost:2901" {
		t.Errorf("accept dial endpoint = %q, want localhost:2901", got.Endpoint)
	}
}

// TestMintAcceptConn_SameRole verifies that when the receiver is on the same
// role as the sender, dialLedgerConcreteFn is NOT called (the sender's
// connection is reused for the accept step).
func TestMintAcceptConn_SameRole(t *testing.T) {
	seedCrossParticipantInstance(t, "xp-mint-same")
	withStubbedSenderDial(t)
	prevRules := findTokenRulesDisclosedFn
	findTokenRulesDisclosedFn = func(_ context.Context, _ *ledger.Client, _ string) (string, *lapiv2.DisclosedContract, error) {
		return "rules::cid", nil, nil
	}
	prevMint := mintViaOfferMintFn
	mintViaOfferMintFn = func(_ context.Context, _ *ledger.Client, _, _, _, _, _ string) (string, error) {
		return "offer::cid", nil
	}
	t.Cleanup(func() {
		findTokenRulesDisclosedFn = prevRules
		mintViaOfferMintFn = prevMint
	})
	acceptDialed := withRecordingAcceptDial(t)

	opts := MintOptions{
		Instance: "xp-mint-same",
		Role:     "app-provider",
		Endpoint: "localhost:3901",
		Insecure: true,
		To:       "alice::1220ab", // same role as sender
		Amount:   "100",
	}
	ref := regstate.TokenRef{IssuerParty: "admin::1111", Symbol: "T1"}
	_ = runMintLive(context.Background(), nil, opts, ref)

	// Sender connection reused; no extra accept-side dial.
	if len(*acceptDialed) != 0 {
		t.Errorf("expected 0 accept-side dials, got %d: %v", len(*acceptDialed), *acceptDialed)
	}
}
