package token

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
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
	// demoV2Capable routes the demo: true → create a new V2 instrument;
	// false → the V1 Amulet demo. A seam so the choreography of each path
	// can be unit-tested without a real registry/catalogue.
	demoV2Capable = v2InstrumentCreateCapable
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
// so its behaviour can't drift from them. A live ledger endpoint is
// required (empty → ErrNeedsV2LocalNet).
//
// The path is chosen by the instance's capability:
//   - a token-standard-v2 instance CAN create a new on-ledger instrument,
//     so the demo creates + mints + seeds a "DEMO" token (the V2 flow);
//   - a standard release instance can only read/transfer the existing V1
//     Amulet, so the demo funds a fresh holder with Amulet moved from the
//     network-funded role party — a transferable token in one click,
//     without needing the alpha token-standard-v2 DAR.
func RunDemo(ctx context.Context, out io.Writer, opts DemoOptions) (*DemoResult, error) {
	if opts.Instance == "" {
		return nil, fmt.Errorf("demo: instance is required")
	}
	if opts.Endpoint == "" {
		return nil, ErrNeedsV2LocalNet
	}
	if demoV2Capable(opts.Instance) {
		return runDemoV2(ctx, out, applyDemoDefaults(opts))
	}
	return runDemoV1(ctx, out, applyDemoV1Defaults(opts))
}

// runDemoV2 creates a new on-ledger V2 instrument, mints its supply, and
// optionally seeds a holder — the one-click demo on a token-standard-v2
// instance. opts is already defaulted.
func runDemoV2(ctx context.Context, out io.Writer, opts DemoOptions) (*DemoResult, error) {
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
		// Re-running the demo with the same symbol is the obvious "click it
		// twice" case — surface it as an actionable conflict rather than a
		// raw "symbol in use". Still wraps ErrSymbolInUse so both surfaces
		// map it to 409.
		if errors.Is(err, ErrSymbolInUse) {
			return nil, fmt.Errorf(
				"a demo token %q already exists on %q — open it on the Tokens screen, "+
					"or relaunch with a different --symbol (%w)",
				opts.Symbol, opts.Instance, ErrSymbolInUse)
		}
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

// runDemoV1 provisions a live, transferable demo on a standard (V1)
// instance. CIP-0056 V1 has no create/mint — Amulet is the only
// instrument and the network-funded role party (app-user) holds it — so
// the demo allocates a fresh holder and moves some Amulet to it via the
// faucet (a funded transfer), giving a transferable balance in one click.
// opts is already defaulted (applyDemoV1Defaults).
func runDemoV1(ctx context.Context, out io.Writer, opts DemoOptions) (*DemoResult, error) {
	step := func(format string, a ...any) {
		if out != nil {
			_, _ = fmt.Fprintf(out, format+"\n", a...)
		}
	}
	// The network's Amulet lives on the role's seeded party (app-user by
	// default); it is the demo's funding source.
	source := roleOrDefault(opts.Role)

	step("Allocating holder party %q…", opts.HolderAlias)
	holder, err := ensureDemoParty(ctx, opts, opts.HolderAlias)
	if err != nil {
		return nil, fmt.Errorf("demo: allocate holder: %w", err)
	}

	step("Funding %s with %s Amulet from %s…", opts.HolderAlias, opts.SeedAmount, source)
	if ferr := demoFaucet(ctx, out, FaucetOptions{
		Instance:   opts.Instance,
		Instrument: amuletSymbol,
		To:         holder.PartyID,
		Amount:     opts.SeedAmount,
		Source:     source,
		Endpoint:   opts.Endpoint,
		Role:       opts.Role,
		Insecure:   opts.Insecure,
	}); ferr != nil {
		return nil, fmt.Errorf("demo: fund holder with Amulet: %w", ferr)
	}

	step("Amulet demo is live and transferable — %s now holds %s Amulet.", opts.HolderAlias, opts.SeedAmount)
	return &DemoResult{
		// Amulet is the pre-existing V1 instrument; there is no created
		// token or minted supply on this path.
		Token:  registry.TokenRef{Name: amuletSymbol, Symbol: amuletSymbol, InstrumentID: amuletSymbol},
		Issuer: registry.PartyRef{Alias: source, Role: roleOrDefault(opts.Role)},
		Holder: holder,
		Seeded: true,
	}, nil
}

// amuletSymbol is the network's V1 instrument used by the V1 demo.
const amuletSymbol = "Amulet"

// v2InstrumentCreateCapable reports whether the instance can create a NEW
// on-ledger V2 instrument — i.e. its Splice version ships the
// splice-test-token-v2 example DAR. Today that is the alpha
// (token-standard-v2) channel; standard releases (0.6.x) can only
// read/transfer the existing V1 Amulet. Unknown/uncurated versions
// default to false so the demo takes the V1 path, which works on any
// running instance.
func v2InstrumentCreateCapable(instance string) bool {
	st, err := registry.Read(instance)
	if err != nil {
		return false
	}
	v, ok := splice.SupportedVersions[st.SpliceVersion]
	return ok && v.IsAlpha()
}

// applyDemoV1Defaults fills the V1-demo tunables. The seed is smaller than
// the V2 default (1000) because it comes out of the funded role party's
// finite genesis Amulet rather than a freshly-minted supply.
func applyDemoV1Defaults(o DemoOptions) DemoOptions {
	if o.SeedAmount == "" {
		o.SeedAmount = "100"
	}
	if o.HolderAlias == "" {
		o.HolderAlias = "demo-holder"
	}
	return o
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
