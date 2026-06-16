package token

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// stubDemoSeams swaps the four RunDemo orchestration seams for fakes and
// returns a restore func — lets us pin the choreography without a ledger.
func stubDemoSeams(
	t *testing.T,
	party func(context.Context, PartyOptions) (*registry.PartyRef, error),
	create func(io.Writer, CreateOptions) (*CreateResult, error),
	mint func(context.Context, io.Writer, MintOptions) error,
	faucet func(context.Context, io.Writer, FaucetOptions) error,
) {
	t.Helper()
	op, oc, om, of := demoPartyNew, demoCreate, demoMint, demoFaucet
	demoPartyNew, demoCreate, demoMint, demoFaucet = party, create, mint, faucet
	t.Cleanup(func() { demoPartyNew, demoCreate, demoMint, demoFaucet = op, oc, om, of })
}

func TestRunDemo_RequiresEndpoint(t *testing.T) {
	if _, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo"}); !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Fatalf("want ErrNeedsV2LocalNet, got %v", err)
	}
}

func TestRunDemo_RequiresInstance(t *testing.T) {
	if _, err := RunDemo(context.Background(), nil, DemoOptions{Endpoint: "localhost:1"}); err == nil {
		t.Fatal("want error for missing instance")
	}
}

func TestRunDemo_ComposesPartyCreateMintFaucet(t *testing.T) {
	var order []string
	var createOpts CreateOptions
	var mintOpts MintOptions
	var faucetOpts FaucetOptions

	stubDemoSeams(t,
		func(_ context.Context, o PartyOptions) (*registry.PartyRef, error) {
			order = append(order, "party:"+o.Alias)
			return &registry.PartyRef{Alias: o.Alias, PartyID: o.Alias + "::pid", Role: o.Role}, nil
		},
		func(_ io.Writer, o CreateOptions) (*CreateResult, error) {
			order = append(order, "create:"+o.Symbol)
			createOpts = o
			return &CreateResult{TokenRef: registry.TokenRef{Symbol: o.Symbol, IssuerParty: o.Issuer, Status: "on-ledger"}}, nil
		},
		func(_ context.Context, _ io.Writer, o MintOptions) error {
			order = append(order, "mint:"+o.To)
			mintOpts = o
			return nil
		},
		func(_ context.Context, _ io.Writer, o FaucetOptions) error {
			order = append(order, "faucet:"+o.To)
			faucetOpts = o
			return nil
		},
	)

	res, err := RunDemo(context.Background(), nil, DemoOptions{
		Instance: "demo", Endpoint: "localhost:5001", Role: "app-user", SeedHolder: true,
	})
	if err != nil {
		t.Fatalf("RunDemo: %v", err)
	}

	want := []string{"party:demo-issuer", "create:DEMO", "mint:demo-issuer::pid", "party:demo-holder", "faucet:demo-holder::pid"}
	if !slices.Equal(order, want) {
		t.Fatalf("call order = %v, want %v", order, want)
	}
	// Issuer party id threads into create (admin), mint (recipient) and the
	// faucet source; defaults applied for symbol/supply/decimals.
	if createOpts.Issuer != "demo-issuer::pid" || createOpts.Symbol != "DEMO" || createOpts.InitialSupply != "1000000" || createOpts.Decimals != 6 {
		t.Errorf("create opts wrong: %+v", createOpts)
	}
	if mintOpts.To != "demo-issuer::pid" || mintOpts.Amount != "1000000" || mintOpts.Instrument != "DEMO" {
		t.Errorf("mint opts wrong: %+v", mintOpts)
	}
	if faucetOpts.Source != "demo-issuer::pid" || faucetOpts.To != "demo-holder::pid" || faucetOpts.Amount != "1000" {
		t.Errorf("faucet opts wrong: %+v", faucetOpts)
	}
	if res.Token.Symbol != "DEMO" || res.Issuer.PartyID != "demo-issuer::pid" || !res.Seeded || res.Holder == nil || res.Holder.PartyID != "demo-holder::pid" {
		t.Errorf("result wrong: %+v", res)
	}
}

func TestRunDemo_NoSeedHolderSkipsFaucet(t *testing.T) {
	var order []string
	stubDemoSeams(t,
		func(_ context.Context, o PartyOptions) (*registry.PartyRef, error) {
			order = append(order, "party:"+o.Alias)
			return &registry.PartyRef{Alias: o.Alias, PartyID: o.Alias + "::pid"}, nil
		},
		func(_ io.Writer, o CreateOptions) (*CreateResult, error) {
			order = append(order, "create")
			return &CreateResult{TokenRef: registry.TokenRef{Symbol: o.Symbol}}, nil
		},
		func(_ context.Context, _ io.Writer, _ MintOptions) error { order = append(order, "mint"); return nil },
		func(_ context.Context, _ io.Writer, _ FaucetOptions) error {
			order = append(order, "faucet")
			return nil
		},
	)

	res, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo", Endpoint: "x:1", SeedHolder: false})
	if err != nil {
		t.Fatalf("RunDemo: %v", err)
	}
	if want := []string{"party:demo-issuer", "create", "mint"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v (no holder/faucet)", order, want)
	}
	if res.Seeded || res.Holder != nil {
		t.Errorf("should not seed a holder: %+v", res)
	}
}

func TestRunDemo_StopsOnCreateError(t *testing.T) {
	minted := false
	stubDemoSeams(t,
		func(_ context.Context, o PartyOptions) (*registry.PartyRef, error) {
			return &registry.PartyRef{Alias: o.Alias, PartyID: "pid"}, nil
		},
		func(_ io.Writer, _ CreateOptions) (*CreateResult, error) { return nil, ErrSymbolInUse },
		func(_ context.Context, _ io.Writer, _ MintOptions) error { minted = true; return nil },
		func(_ context.Context, _ io.Writer, _ FaucetOptions) error { return nil },
	)

	if _, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo", Endpoint: "x:1", SeedHolder: true}); !errors.Is(err, ErrSymbolInUse) {
		t.Fatalf("want ErrSymbolInUse wrapped, got %v", err)
	}
	if minted {
		t.Error("mint must not run after create fails")
	}
}

func TestRunDemo_ReusesExistingIssuerAlias(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "0.6.4")
	s.Parties = map[string]registry.PartyRef{"demo-issuer": {Alias: "demo-issuer", PartyID: "existing::pid"}}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var createIssuer string
	stubDemoSeams(t,
		func(_ context.Context, o PartyOptions) (*registry.PartyRef, error) {
			return nil, fmt.Errorf("%w: %q", ErrAliasInUse, o.Alias)
		},
		func(_ io.Writer, o CreateOptions) (*CreateResult, error) {
			createIssuer = o.Issuer
			return &CreateResult{TokenRef: registry.TokenRef{Symbol: o.Symbol}}, nil
		},
		func(_ context.Context, _ io.Writer, _ MintOptions) error { return nil },
		func(_ context.Context, _ io.Writer, _ FaucetOptions) error { return nil },
	)

	if _, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo", Endpoint: "x:1", SeedHolder: false}); err != nil {
		t.Fatalf("RunDemo: %v", err)
	}
	if createIssuer != "existing::pid" {
		t.Errorf("should reuse the existing demo-issuer party, got %q", createIssuer)
	}
}
