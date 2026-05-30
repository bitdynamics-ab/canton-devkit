package token

import (
	"context"
	"errors"
	"testing"
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

	rows, err := RunBalance(context.Background(), nil, BalanceOptions{Instance: "demo"})
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

	rows, err := RunBalance(context.Background(), nil, BalanceOptions{
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

	all, err := RunBalance(context.Background(), nil, BalanceOptions{Instance: "demo"})
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 rows for all instruments, got %d (%v)", len(all), err)
	}

	one, err := RunBalance(context.Background(), nil, BalanceOptions{
		Instance: "demo", Instrument: "RTK",
	})
	if err != nil || len(one) != 1 || one[0].InstrumentSymbol != "RTK" {
		t.Errorf("--instrument RTK filter: rows=%+v err=%v", one, err)
	}
}

// TestRunMint_NotWiredYet pins the BIT-139 follow-up boundary: the
// action validates + resolves the instrument, but returns
// ErrNeedsV2LocalNet so callers can render the remediation. Once the
// live ledger submission lands this test changes shape, not name.
func TestRunMint_NotWiredYet(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if _, err := RunCreate(nil, happyOpts("demo")); err != nil {
		t.Fatalf("create: %v", err)
	}

	err := RunMint(context.Background(), nil, MintOptions{
		Instance: "demo", Instrument: "RTK", To: "bob::xyz", Amount: "100",
	})
	if !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Errorf("RunMint should surface ErrNeedsV2LocalNet until V2 submit lands; got %v", err)
	}

	// Missing required field is rejected BEFORE the V2-needed error,
	// so callers can distinguish "you forgot --to" from "V2 isn't up".
	err = RunMint(context.Background(), nil, MintOptions{
		Instance: "demo", Instrument: "RTK", Amount: "100",
	})
	if errors.Is(err, ErrNeedsV2LocalNet) {
		t.Errorf("missing --to should be a validation error, not V2-needed; got %v", err)
	}
}

// TestRunTransfer_UnknownSymbol surfaces the 'use token create
// first' hint, not the V2-needed remediation — getting the wording
// wrong here is a usability bug.
func TestRunTransfer_UnknownSymbol(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	err := RunTransfer(context.Background(), nil, TransferOptions{
		Instance: "demo", Instrument: "GHOST", From: "a", To: "b", Amount: "1",
	})
	if err == nil {
		t.Fatal("expected error for unknown symbol")
	}
	if errors.Is(err, ErrNeedsV2LocalNet) {
		t.Errorf("unknown symbol should NOT surface V2-needed; got %v", err)
	}
}
