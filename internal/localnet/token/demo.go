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

	// Party aliases; default demo-issuer / demo-holder.
	IssuerAlias string
	HolderAlias string
}

// DemoResult is the outcome of RunDemo, shared by the CLI `token demo
// --format json` and POST /api/tokens/demo so both surfaces emit one shape.
type DemoResult struct {
	Token  registry.TokenRef  `json:"token"`
	Issuer registry.PartyRef  `json:"issuer"`
	Holder *registry.PartyRef `json:"holder,omitempty"`
	Seeded bool               `json:"seeded"`
}

// Orchestration seams — package vars so RunDemo's choreography can be
// unit-tested without a live ledger. Default to the real Run* functions.
var (
	demoPartyNew = RunPartyNew
	demoCreate   = RunCreate
	demoMint     = RunMint
	demoFaucet   = RunFaucet
	// demoV2Capable routes the demo: true → new V2 instrument, false → V1 Amulet.
	demoV2Capable = v2InstrumentCreateCapable
)

// RunDemo provisions a live, transferable demo token in one call, composing
// the same Run* verbs the CLI/UI use so it can't drift. A live ledger endpoint
// is required (empty → ErrNeedsV2LocalNet). The path is capability-chosen:
//   - token-standard-v2 instance: create + mint + seed a new "DEMO" instrument;
//   - standard (V1) instance: fund a fresh holder with Amulet from the
//     network-funded role party (no create/mint exists there).
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

// runDemoV2 creates a V2 instrument, mints its supply, and optionally seeds a
// holder. opts is already defaulted.
func runDemoV2(ctx context.Context, out io.Writer, opts DemoOptions) (*DemoResult, error) {
	step := func(format string, a ...any) {
		if out != nil {
			_, _ = fmt.Fprintf(out, format+"\n", a...)
		}
	}

	step("Allocating issuer party %q…", opts.IssuerAlias)
	issuer, err := ensureDemoParty(ctx, opts, opts.IssuerAlias)
	if err != nil {
		return nil, fmt.Errorf("demo: allocate issuer: %w", err)
	}

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
		// The "click it twice" case: give an actionable message but still
		// wrap ErrSymbolInUse so both surfaces map it to 409.
		if errors.Is(err, ErrSymbolInUse) {
			return nil, fmt.Errorf(
				"a demo token %q already exists on %q — open it on the Tokens screen, "+
					"or relaunch with a different --symbol (%w)",
				opts.Symbol, opts.Instance, ErrSymbolInUse)
		}
		return nil, fmt.Errorf("demo: create instrument: %w", err)
	}

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

// ensureDemoParty allocates a party by alias, reusing an existing one on
// ErrAliasInUse so a re-run doesn't fail on a name clash.
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
		// Reuse the party recorded on a prior run so the demo is idempotent.
		if state, rerr := registry.Read(opts.Instance); rerr == nil {
			if existing, ok := state.Parties[alias]; ok {
				return &existing, nil
			}
		}
	}
	return nil, err
}

// runDemoV1 provisions a demo on a standard (V1) instance: with no create/mint
// (Amulet is the only instrument, held by the network-funded role party), it
// funds a fresh holder via a faucet transfer. opts is already defaulted.
func runDemoV1(ctx context.Context, out io.Writer, opts DemoOptions) (*DemoResult, error) {
	step := func(format string, a ...any) {
		if out != nil {
			_, _ = fmt.Fprintf(out, format+"\n", a...)
		}
	}
	// The role's seeded party (app-user by default) holds the Amulet.
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
		Token:  registry.TokenRef{Name: amuletSymbol, Symbol: amuletSymbol, InstrumentID: amuletSymbol},
		Issuer: registry.PartyRef{Alias: source, Role: roleOrDefault(opts.Role)},
		Holder: holder,
		Seeded: true,
	}, nil
}

const amuletSymbol = "Amulet"

// v2InstrumentCreateCapable reports whether the instance can create a new V2
// instrument (its Splice version ships the splice-test-token-v2 DAR, i.e. the
// alpha channel). Unknown versions default to false → the V1 path.
func v2InstrumentCreateCapable(instance string) bool {
	st, err := registry.Read(instance)
	if err != nil {
		return false
	}
	v, ok := splice.SupportedVersions[st.SpliceVersion]
	return ok && v.IsAlpha()
}

// applyDemoV1Defaults fills the V1-demo tunables. The seed is smaller than the
// V2 default since it comes from the role party's finite genesis Amulet.
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
