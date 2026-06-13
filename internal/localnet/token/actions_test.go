package token

import (
	"context"
	"errors"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
)

// TestRunBalance_DerivesFromRegistry covers the only action that's
// fully functional today: balance reads state.Tokens and returns
// pseudo-balances (full InitialSupply to the issuer, 0 to others).
// This pins the contract until the live ACS-derived balance lands.
func TestRunBalance_DerivesFromRegistry(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if _, err := RunCreate(nil, happyOpts("demo")); err != nil {
		t.Fatalf("create: %v", err)
	}

	rows, _, err := RunBalance(context.Background(), nil, BalanceOptions{Instance: "demo"})
	if err != nil {
		t.Fatalf("RunBalance: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.InstrumentSymbol != "RTK" || r.Party != "alice::abc" || r.Amount != "1000000" {
		t.Errorf("issuer pseudo-balance drifted: %+v", r)
	}
}

// TestRunBalance_NonIssuerSeesZero — the contract for the
// not-yet-wired phase: non-issuer parties see zero, not the
// InitialSupply. Otherwise the UI's balance card would mislead.
func TestRunBalance_NonIssuerSeesZero(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if _, err := RunCreate(nil, happyOpts("demo")); err != nil {
		t.Fatalf("create: %v", err)
	}

	rows, _, err := RunBalance(context.Background(), nil, BalanceOptions{
		Instance: "demo", Party: "bob::xyz",
	})
	if err != nil {
		t.Fatalf("RunBalance: %v", err)
	}
	if len(rows) != 1 || rows[0].Amount != "0" {
		t.Errorf("non-issuer party should see zero; got %+v", rows)
	}
}

// TestRunBalance_FilterByInstrument: --instrument symbol|id filters
// the response. Empty filter returns all instruments.
func TestRunBalance_FilterByInstrument(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if _, err := RunCreate(nil, happyOpts("demo")); err != nil {
		t.Fatalf("create RTK: %v", err)
	}
	second := happyOpts("demo")
	second.Symbol = "STK"
	if _, err := RunCreate(nil, second); err != nil {
		t.Fatalf("create STK: %v", err)
	}

	all, _, err := RunBalance(context.Background(), nil, BalanceOptions{Instance: "demo"})
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 rows for all instruments, got %d (%v)", len(all), err)
	}

	one, _, err := RunBalance(context.Background(), nil, BalanceOptions{
		Instance: "demo", Instrument: "RTK",
	})
	if err != nil || len(one) != 1 || one[0].InstrumentSymbol != "RTK" {
		t.Errorf("--instrument RTK filter: rows=%+v err=%v", one, err)
	}
}

// TestRunMint_UnsupportedOnInstrument pins the V2-alpha behaviour:
// Amulet (the only seeded instrument) doesn't implement
// BurnMintFactoryV1, and V2 has no generic mint interface, so RunMint
// surfaces ErrUnsupportedOnInstrument. Validation errors (missing
// --to) must still preempt — callers need to distinguish "you forgot
// --to" from "this asset can't be minted".
func TestRunMint_UnsupportedOnInstrument(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if _, err := RunCreate(nil, happyOpts("demo")); err != nil {
		t.Fatalf("create: %v", err)
	}

	err := RunMint(context.Background(), nil, MintOptions{
		Instance: "demo", Instrument: "RTK", To: "bob::xyz", Amount: "100",
	})
	if !errors.Is(err, ErrUnsupportedOnInstrument) {
		t.Errorf("RunMint should surface ErrUnsupportedOnInstrument for assets without standard mint; got %v", err)
	}

	// Missing required field is rejected BEFORE the unsupported error,
	// so callers can distinguish "you forgot --to" from "asset can't mint".
	err = RunMint(context.Background(), nil, MintOptions{
		Instance: "demo", Instrument: "RTK", Amount: "100",
	})
	if errors.Is(err, ErrUnsupportedOnInstrument) {
		t.Errorf("missing --to should be a validation error, not unsupported-instrument; got %v", err)
	}
}

// TestRunTransfer_UnknownSymbol covers the no-endpoint path: when no
// live endpoint is set, RunTransfer falls back to ErrNeedsV2LocalNet
// after passing field-validation. With an unknown symbol AND no
// endpoint, ErrNeedsV2LocalNet is the deliberate surface (so the user
// adds --endpoint and re-runs, where the orchestration treats unknown
// symbols as raw instrument ids).
func TestRunTransfer_UnknownSymbol(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	err := RunTransfer(context.Background(), nil, TransferOptions{
		Instance: "demo", Instrument: "GHOST", From: "a", To: "b", Amount: "1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Errorf("no-endpoint transfer should surface ErrNeedsV2LocalNet; got %v", err)
	}
}

// --- #63: holdings provenance (live ACS vs registry fallback) -------

// setLedgerPort records a participant_ledger_<role> port on an
// already-seeded instance so ResolveLedgerEndpoint / BalanceSource see
// a live endpoint without a real participant.
func setLedgerPort(t *testing.T, instance, role string, port int) {
	t.Helper()
	st, err := registry.Read(instance)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	st.Ports["participant_ledger_"+role] = port
	if err := registry.Write(st); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

// TestRunBalance_RegistryFallbackTaggedRegistry pins #63: with no
// captured ledger port (instance down / pre-port-capture), RunBalance
// takes the pseudo-balance path AND every row is tagged
// SourceRegistry so the surfaces can flag it as not-on-ledger.
func TestRunBalance_RegistryFallbackTaggedRegistry(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if _, err := RunCreate(nil, happyOpts("demo")); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows, _, err := RunBalance(context.Background(), nil, BalanceOptions{Instance: "demo"})
	if err != nil {
		t.Fatalf("RunBalance: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Source != SourceRegistry {
		t.Errorf("registry-fallback row Source = %q, want %q", rows[0].Source, SourceRegistry)
	}
}

// TestBalanceSource pins the pure source-selection contract both
// surfaces rely on to set the response-level provenance + banner.
func TestBalanceSource(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	// No port, no explicit endpoint → registry.
	if got := BalanceSource(BalanceOptions{Instance: "demo", Role: "app-user"}); got != SourceRegistry {
		t.Errorf("no-port BalanceSource = %q, want %q", got, SourceRegistry)
	}
	// Explicit endpoint always wins → ledger.
	if got := BalanceSource(BalanceOptions{Instance: "demo", Endpoint: "fake:0"}); got != SourceLedger {
		t.Errorf("explicit-endpoint BalanceSource = %q, want %q", got, SourceLedger)
	}
	// Captured port → ledger (auto-discovery), even with no explicit endpoint.
	setLedgerPort(t, "demo", "app-user", 2901)
	if got := BalanceSource(BalanceOptions{Instance: "demo", Role: "app-user"}); got != SourceLedger {
		t.Errorf("captured-port BalanceSource = %q, want %q", got, SourceLedger)
	}
}

// TestRunBalanceLive_TagsRowsLedger pins #63's other half: when the
// live ACS yields a holding, the summed row is tagged SourceLedger.
// Uses the fakeLedger seam + a single HoldingViewV2-shaped created
// event so we exercise the real row-construction path.
func TestRunBalanceLive_TagsRowsLedger(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	fake := &fakeLedger{
		ListKnownPartiesFn: func(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
			return &adminv2.ListKnownPartiesResponse{
				PartyDetails: []*adminv2.PartyDetails{{Party: "app_user_alice", IsLocal: true}},
			}, nil
		},
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream([]*lapiv2.GetActiveContractsResponse{
				holdingContract("app_user_alice", "USD", "issuer::adm", "42.0"),
			}, nil), nil
		},
	}
	withFakeDial(t, fake)

	rows, _, err := runBalanceLive(context.Background(), liveOpts("demo", "app-user"))
	if err != nil {
		t.Fatalf("runBalanceLive: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Amount != "42.0" || rows[0].Party != "app_user_alice" {
		t.Errorf("row drifted: %+v", rows[0])
	}
	if rows[0].Source != SourceLedger {
		t.Errorf("live row Source = %q, want %q", rows[0].Source, SourceLedger)
	}
}

// holdingContract builds a GetActiveContractsResponse carrying a
// single HoldingViewV2-shaped InterfaceView (account.owner,
// instrumentId.{admin,id}, amount) so extractHoldingViewV2 parses it.
// Reuses the production value_builders.go constructors so the test
// view matches the shape the real participant emits.
func holdingContract(owner, instrumentID, admin, amount string) *lapiv2.GetActiveContractsResponse {
	view := recordValue([]field{
		{"account", recordValue([]field{
			{"owner", optionalPartyValue(&owner)},
		})},
		{"instrumentId", recordValue([]field{
			{"admin", partyValue(admin)},
			{"id", textValue(instrumentID)},
		})},
		{"amount", numericValue(amount)},
	})
	return &lapiv2.GetActiveContractsResponse{
		ContractEntry: &lapiv2.GetActiveContractsResponse_ActiveContract{
			ActiveContract: &lapiv2.ActiveContract{
				CreatedEvent: &lapiv2.CreatedEvent{
					InterfaceViews: []*lapiv2.InterfaceView{{ViewValue: view.GetRecord()}},
				},
			},
		},
	}
}
