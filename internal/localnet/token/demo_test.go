package token

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// stubDemoSeams swaps the four RunDemo orchestration seams for fakes.
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

// stubDemoV2Capable pins the V1/V2 routing decision.
func stubDemoV2Capable(t *testing.T, v bool) {
	t.Helper()
	prev := demoV2Capable
	demoV2Capable = func(string) bool { return v }
	t.Cleanup(func() { demoV2Capable = prev })
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
	stubDemoV2Capable(t, true)
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
			faucetOpts = o
			order = append(order, "faucet:"+o.To)
			return nil
		},
	)

	res, err := RunDemo(context.Background(), nil, DemoOptions{
		Instance: "demo", Endpoint: "localhost:5001", Role: "app-user",
	})
	if err != nil {
		t.Fatalf("RunDemo: %v", err)
	}

	// The V2 supply is minted to the holder (never the issuer — the test
	// token can't self-mint), so there's no faucet leg.
	want := []string{"party:demo-issuer", "create:DEMO", "party:demo-holder", "mint:demo-holder::pid"}
	if !slices.Equal(order, want) {
		t.Fatalf("call order = %v, want %v", order, want)
	}
	if createOpts.Issuer != "demo-issuer::pid" || createOpts.Symbol != "DEMO" || createOpts.InitialSupply != "1000000" || createOpts.Decimals != 6 {
		t.Errorf("create opts wrong: %+v", createOpts)
	}
	if mintOpts.To != "demo-holder::pid" || mintOpts.Amount != "1000000" || mintOpts.Instrument != "DEMO" {
		t.Errorf("mint opts wrong: %+v", mintOpts)
	}
	if len(faucetOpts.To) != 0 {
		t.Errorf("V2 demo must not faucet: %+v", faucetOpts)
	}
	if res.Token.Symbol != "DEMO" || res.Issuer.PartyID != "demo-issuer::pid" || !res.Seeded || res.Holder == nil || res.Holder.PartyID != "demo-holder::pid" {
		t.Errorf("result wrong: %+v", res)
	}
}

// The V2 demo has no faucet leg: the supply is minted straight to the holder
// (the issuer can't self-mint), so the holder is always present and seeded.
func TestRunDemo_V2MintsToHolderNoFaucet(t *testing.T) {
	stubDemoV2Capable(t, true)
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
		func(_ context.Context, _ io.Writer, o MintOptions) error {
			order = append(order, "mint:"+o.To)
			return nil
		},
		func(_ context.Context, _ io.Writer, _ FaucetOptions) error {
			order = append(order, "faucet")
			return nil
		},
	)

	res, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo", Endpoint: "x:1"})
	if err != nil {
		t.Fatalf("RunDemo: %v", err)
	}
	if want := []string{"party:demo-issuer", "create", "party:demo-holder", "mint:demo-holder::pid"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v (mint to holder, no faucet)", order, want)
	}
	if !res.Seeded || res.Holder == nil || res.Holder.PartyID != "demo-holder::pid" {
		t.Errorf("V2 demo should always seed the holder: %+v", res)
	}
}

func TestRunDemo_StopsOnCreateError(t *testing.T) {
	stubDemoV2Capable(t, true)
	minted := false
	stubDemoSeams(t,
		func(_ context.Context, o PartyOptions) (*registry.PartyRef, error) {
			return &registry.PartyRef{Alias: o.Alias, PartyID: "pid"}, nil
		},
		func(_ io.Writer, _ CreateOptions) (*CreateResult, error) { return nil, ErrSymbolInUse },
		func(_ context.Context, _ io.Writer, _ MintOptions) error { minted = true; return nil },
		func(_ context.Context, _ io.Writer, _ FaucetOptions) error { return nil },
	)

	if _, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo", Endpoint: "x:1"}); !errors.Is(err, ErrSymbolInUse) {
		t.Fatalf("want ErrSymbolInUse wrapped, got %v", err)
	}
	if minted {
		t.Error("mint must not run after create fails")
	}
}

// Re-run must give an actionable "already exists" message while still wrapping
// ErrSymbolInUse (→ 409 on both surfaces).
func TestRunDemo_DuplicateSymbolIsActionable(t *testing.T) {
	stubDemoV2Capable(t, true)
	stubDemoSeams(t,
		func(_ context.Context, o PartyOptions) (*registry.PartyRef, error) {
			return &registry.PartyRef{Alias: o.Alias, PartyID: "pid"}, nil
		},
		func(_ io.Writer, _ CreateOptions) (*CreateResult, error) { return nil, ErrSymbolInUse },
		func(_ context.Context, _ io.Writer, _ MintOptions) error { return nil },
		func(_ context.Context, _ io.Writer, _ FaucetOptions) error { return nil },
	)

	_, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo", Endpoint: "x:1"})
	if !errors.Is(err, ErrSymbolInUse) {
		t.Fatalf("must still wrap ErrSymbolInUse (-> 409), got %v", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("re-run error should be actionable (\"already exists\"), got %q", err.Error())
	}
}

func TestRunDemo_ReusesExistingIssuerAlias(t *testing.T) {
	stubDemoV2Capable(t, true)
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "0.6.4")
	s.Parties = map[string]registry.PartyRef{
		"demo-issuer": {Alias: "demo-issuer", PartyID: "existing::pid"},
		"demo-holder": {Alias: "demo-holder", PartyID: "existing-holder::pid"},
	}
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

	if _, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo", Endpoint: "x:1"}); err != nil {
		t.Fatalf("RunDemo: %v", err)
	}
	if createIssuer != "existing::pid" {
		t.Errorf("should reuse the existing demo-issuer party, got %q", createIssuer)
	}
}

// On a V1 instance the demo faucets Amulet to a holder — no create, no mint.
func TestRunDemo_V1FundsHolderWithAmulet(t *testing.T) {
	stubDemoV2Capable(t, false)
	var order []string
	var faucetOpts FaucetOptions
	stubDemoSeams(t,
		func(_ context.Context, o PartyOptions) (*registry.PartyRef, error) {
			order = append(order, "party:"+o.Alias)
			return &registry.PartyRef{Alias: o.Alias, PartyID: o.Alias + "::pid", Role: o.Role}, nil
		},
		func(_ io.Writer, o CreateOptions) (*CreateResult, error) {
			order = append(order, "create")
			return &CreateResult{TokenRef: registry.TokenRef{Symbol: o.Symbol}}, nil
		},
		func(_ context.Context, _ io.Writer, _ MintOptions) error { order = append(order, "mint"); return nil },
		func(_ context.Context, _ io.Writer, o FaucetOptions) error {
			order = append(order, "faucet:"+o.To)
			faucetOpts = o
			return nil
		},
	)

	res, err := RunDemo(context.Background(), nil, DemoOptions{
		Instance: "demo", Endpoint: "localhost:5001", Role: "app-user",
	})
	if err != nil {
		t.Fatalf("RunDemo: %v", err)
	}

	if want := []string{"party:demo-holder", "faucet:demo-holder::pid"}; !slices.Equal(order, want) {
		t.Fatalf("V1 call order = %v, want %v (no create/mint)", order, want)
	}
	if faucetOpts.Instrument != "Amulet" || faucetOpts.Source != "app-user" ||
		faucetOpts.To != "demo-holder::pid" || faucetOpts.Amount != "100" {
		t.Errorf("V1 faucet opts wrong: %+v", faucetOpts)
	}
	if res.Token.Symbol != "Amulet" || res.Holder == nil || res.Holder.PartyID != "demo-holder::pid" || !res.Seeded {
		t.Errorf("V1 result wrong: %+v", res)
	}
}
