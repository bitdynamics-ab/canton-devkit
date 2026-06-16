package token

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// DemoOptions configures the one-click demo-token provisioning.
type DemoOptions struct {
	Instance string
	Role     string
	Endpoint string // live participant ledger endpoint; required (V2 instance)
	Insecure bool

	// Tunables — defaults applied when empty/zero.
	Symbol        string // default "DEMO"
	Name          string // default "Demo Token"
	Decimals      int    // default 6
	InitialSupply string // default "1000000"
	SeedHolder    bool   // allocate a holder party + fund it so it's transferable
	SeedAmount    string // default "1000"

	// Aliases for the provisioned parties. Exposed so a caller can target
	// distinct parties; defaults are demo-issuer / demo-holder.
	IssuerAlias string
	HolderAlias string
}

// DemoResult is the outcome of RunDemo: the created instrument, the
// issuer party, and (when seeded) the funded holder. Shared by the CLI
// `token demo --format json` and POST /api/tokens/demo so both surfaces
// emit an identical shape.
type DemoResult struct {
	Token  registry.TokenRef  `json:"token"`
	Issuer registry.PartyRef  `json:"issuer"`
	Holder *registry.PartyRef `json:"holder,omitempty"`
	Seeded bool               `json:"seeded"`
}

// Orchestration seams — package vars so RunDemo's choreography can be
// unit-tested without a live ledger (mirrors the runTokenCreate
// indirection the UI handlers use). Default to the real Run* functions.
var (
	demoPartyNew = RunPartyNew
	demoCreate   = RunCreate
	demoMint     = RunMint
	demoFaucet   = RunFaucet
)

// RunDemo provisions a live, transferable demo token in one call:
//
//	allocate an issuer party
//	  → create a V2 instrument on-ledger (issuer = admin)
//	  → mint the initial supply to the issuer (create records the
//	    instrument but does NOT mint)
//	  → optionally allocate a holder party and faucet it some tokens so a
//	    transfer works immediately.
//
// It composes the same Run* functions the individual CLI/UI verbs use,
// so its behaviour can't drift from them. A live V2 endpoint is required
// (there's no on-ledger instrument to mint/transfer otherwise) — without
// one it returns ErrNeedsV2LocalNet, which both surfaces map to a "start
// a V2 instance first" remediation.
func RunDemo(ctx context.Context, out io.Writer, opts DemoOptions) (*DemoResult, error) {
	if opts.Instance == "" {
		return nil, fmt.Errorf("demo: instance is required")
	}
	if opts.Endpoint == "" {
		return nil, ErrNeedsV2LocalNet
	}
	opts = applyDemoDefaults(opts)

	step := func(format string, a ...any) {
		if out != nil {
			_, _ = fmt.Fprintf(out, format+"\n", a...)
		}
	}

	// 1. Issuer party (idempotent: reuse an existing alias on a re-run).
	step("Allocating issuer party %q…", opts.IssuerAlias)
	issuer, err := ensureDemoParty(ctx, opts, opts.IssuerAlias)
	if err != nil {
		return nil, fmt.Errorf("demo: allocate issuer: %w", err)
	}

	// 2. Create the V2 instrument on-ledger (issuer is the admin).
	step("Creating %s (supply %s, %d decimals)…", opts.Symbol, opts.InitialSupply, opts.Decimals)
	created, err := demoCreate(out, CreateOptions{
		Instance:      opts.Instance,
		Name:          opts.Name,
		Symbol:        opts.Symbol,
		Decimals:      opts.Decimals,
		InitialSupply: opts.InitialSupply,
		Issuer:        issuer.PartyID,
		Endpoint:      opts.Endpoint,
		Role:          opts.Role,
		Insecure:      opts.Insecure,
	})
	if err != nil {
		return nil, fmt.Errorf("demo: create instrument: %w", err)
	}

	// 3. Mint the initial supply to the issuer.
	step("Minting %s %s to the issuer…", opts.InitialSupply, opts.Symbol)
	if err := demoMint(ctx, out, MintOptions{
		Instance:   opts.Instance,
		Instrument: opts.Symbol,
		To:         issuer.PartyID,
		Amount:     opts.InitialSupply,
		Endpoint:   opts.Endpoint,
		Role:       opts.Role,
		Insecure:   opts.Insecure,
	}); err != nil {
		return nil, fmt.Errorf("demo: mint supply: %w", err)
	}

	result := &DemoResult{Token: created.TokenRef, Issuer: *issuer}

	// 4. Optionally seed a holder so the token is transferable in one click.
	if opts.SeedHolder {
		step("Allocating holder party %q…", opts.HolderAlias)
		holder, herr := ensureDemoParty(ctx, opts, opts.HolderAlias)
		if herr != nil {
			return nil, fmt.Errorf("demo: allocate holder: %w", herr)
		}
		step("Funding %s with %s %s from the issuer…", opts.HolderAlias, opts.SeedAmount, opts.Symbol)
		if ferr := demoFaucet(ctx, out, FaucetOptions{
			Instance:   opts.Instance,
			Instrument: opts.Symbol,
			To:         holder.PartyID,
			Amount:     opts.SeedAmount,
			Source:     issuer.PartyID,
			Endpoint:   opts.Endpoint,
			Role:       opts.Role,
			Insecure:   opts.Insecure,
		}); ferr != nil {
			return nil, fmt.Errorf("demo: fund holder: %w", ferr)
		}
		result.Holder = holder
		result.Seeded = true
	}

	step("Demo token %s is live%s.", opts.Symbol, map[bool]string{true: " and transferable", false: ""}[opts.SeedHolder])
	return result, nil
}

// ensureDemoParty allocates a party by alias, reusing an existing one
// (ErrAliasInUse) so re-running the demo doesn't fail on a name clash.
func ensureDemoParty(ctx context.Context, opts DemoOptions, alias string) (*registry.PartyRef, error) {
	ref, err := demoPartyNew(ctx, PartyOptions{
		Instance: opts.Instance,
		Alias:    alias,
		Endpoint: opts.Endpoint,
		Role:     opts.Role,
		Insecure: opts.Insecure,
	})
	if err == nil {
		return ref, nil
	}
	if errors.Is(err, ErrAliasInUse) {
		// Already allocated on a prior run — reuse the recorded party so
		// the demo is idempotent rather than failing the whole flow.
		if state, rerr := registry.Read(opts.Instance); rerr == nil {
			if existing, ok := state.Parties[alias]; ok {
				return &existing, nil
			}
		}
	}
	return nil, err
}

func applyDemoDefaults(o DemoOptions) DemoOptions {
	if o.Symbol == "" {
		o.Symbol = "DEMO"
	}
	if o.Name == "" {
		o.Name = "Demo Token"
	}
	if o.Decimals == 0 {
		o.Decimals = 6
	}
	if o.InitialSupply == "" {
		o.InitialSupply = "1000000"
	}
	if o.SeedAmount == "" {
		o.SeedAmount = "1000"
	}
	if o.IssuerAlias == "" {
		o.IssuerAlias = "demo-issuer"
	}
	if o.HolderAlias == "" {
		o.HolderAlias = "demo-holder"
	}
	return o
}
