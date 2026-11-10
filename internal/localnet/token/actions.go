package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

var ErrNeedsV2LocalNet = errors.New(
	"V2 ledger action needs a running Splice 0.6.11 or newer LocalNet; " +
		"bring one up with `localnet up --version 0.6.12`, then re-run")

var ErrUnsupportedOnInstrument = errors.New(
	"this instrument doesn't implement the V2 standard's mint/burn surface " +
		"(Amulet has no BurnMintFactoryV1, and V2 defines no generic mint/burn). " +
		"Use the asset's own wallet UI for issuance changes, or create a " +
		"splice-test-token-v2 instrument via `localnet token create` for a token " +
		"that implements asset-specific mint via TokenRules_OfferMint")

var ErrUnresolvedLedgerEndpoint = errors.New("unresolved ledger endpoint")

func unresolvedLedgerEndpoint(instance, role string) error {
	return fmt.Errorf(
		"%w: instance %q role %q has no recorded participant ledger port — "+
			"restart it (`localnet down %s` then `localnet up %s`) so ports are captured, "+
			"or pass --endpoint host:port",
		ErrUnresolvedLedgerEndpoint, instance, role, instance, instance)
}

type MintOptions struct {
	Instance   string
	Instrument string
	To         string
	Amount     string

	Endpoint string
	Role     string
	Insecure bool
}
type TransferOptions struct {
	Instance   string
	Instrument string
	From       string
	To         string
	Amount     string
	NoWait     bool
	Reason     string

	AutoAccept bool

	Atomic bool

	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string
}
type AcceptOptions struct {
	Instance              string
	TransferInstructionID string

	Party string

	Gen Generation

	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string
}
type BurnOptions struct {
	Instance   string
	Instrument string
	From       string
	Amount     string

	Endpoint string
	Token    string
	Role     string
	Insecure bool
}
type BalanceOptions struct {
	Instance   string
	Party      string
	Instrument string

	Endpoint string
	Token    string
	Role     string
	Insecure bool

	Limit int
}

const maxHoldingsScan = 10_000

type BalanceRow struct {
	InstrumentSymbol string        `json:"instrument_symbol,omitempty"`
	InstrumentID     string        `json:"instrument_id"`
	Party            string        `json:"party"`
	Amount           string        `json:"amount"`
	Source           HoldingSource `json:"source"`
}

type HoldingSource string

const (
	SourceLedger   HoldingSource = "ledger"
	SourceRegistry HoldingSource = "registry"
)

func RunMint(ctx context.Context, out io.Writer, opts MintOptions) error {
	if err := requireFields("mint", "instance", opts.Instance, "instrument", opts.Instrument,
		"recipient party", opts.To, "amount", opts.Amount); err != nil {
		return err
	}
	opts.To = ResolveAlias(aliasMapForInstance(opts.Instance), opts.To)
	if err := validatePartyID("--to", opts.To); err != nil {
		return err
	}
	if err := validateAmount("mint", opts.Amount); err != nil {
		return err
	}
	if opts.Role == "" {
		opts.Role = DefaultRole
	}
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint == "" {
		emit(out, "mint", map[string]any{
			"instance": opts.Instance, "role": opts.Role,
			"endpoint": "", "to": opts.To, "amount": opts.Amount,
		})
		return unresolvedLedgerEndpoint(opts.Instance, opts.Role)
	}
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	if ref.Status == "on-ledger" {
		if err := enforceSupplyCap(ctx, opts, ref); err != nil {
			return err
		}
		return runMintLive(ctx, out, opts, ref)
	}
	emit(out, "mint", map[string]any{
		"instrument": ref, "to": opts.To, "amount": opts.Amount,
		"endpoint": opts.Endpoint, "role": opts.Role,
	})
	return ErrUnsupportedOnInstrument
}

func RunTransfer(ctx context.Context, out io.Writer, opts TransferOptions) error {
	if err := requireFields("transfer", "instance", opts.Instance, "instrument", opts.Instrument,
		"sender party", opts.From, "recipient party", opts.To, "amount", opts.Amount); err != nil {
		return err
	}
	aliases := aliasMapForInstance(opts.Instance)
	opts.From = ResolveAlias(aliases, opts.From)
	opts.To = ResolveAlias(aliases, opts.To)
	if err := validatePartyID("--from", opts.From); err != nil {
		return err
	}
	if err := validatePartyID("--to", opts.To); err != nil {
		return err
	}
	if err := validateAmount("transfer", opts.Amount); err != nil {
		return err
	}
	if opts.Role == "" {
		opts.Role = DefaultRole
	}
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint == "" {
		emit(out, "transfer", map[string]any{
			"from": opts.From, "to": opts.To, "amount": opts.Amount,
		})
		return ErrNeedsV2LocalNet
	}

	// On-ledger instruments transfer by exercising the issuer's TokenRules
	// contract (which IS the V2 TransferFactory, admin = issuer) directly.
	// The off-ledger path targets the scan registry's Amulet factory
	// (admin = DSO), which rejects an issuer-administered instrument with
	// an admin-mismatch assertion.
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	if ref.Status == "on-ledger" {
		instructionID, err := runTransferOnLedgerFn(ctx, out, opts, ref)
		if err != nil {
			return err
		}
		emit(out, "transfer complete", map[string]any{
			"transfer_instruction_id": instructionID,
		})
		return nil
	}
	return runTransferOffLedgerFn(ctx, out, opts)
}

func runTransferOffLedger(ctx context.Context, out io.Writer, opts TransferOptions) error {
	instructionID, gen, err := runTransferLive(ctx, out, opts)
	if err != nil {
		return err
	}
	emit(out, "transfer complete", map[string]any{
		"transfer_instruction_id": instructionID,
	})

	if opts.AutoAccept && !opts.NoWait && instructionID != "" {
		acc := AcceptOptions{
			Instance:              opts.Instance,
			TransferInstructionID: instructionID,
			Party:                 opts.To,
			Gen:                   gen,
			Endpoint:              opts.Endpoint,
			Token:                 opts.Token,
			Role:                  opts.Role,
			Insecure:              opts.Insecure,
			RegistryURL:           opts.RegistryURL,
		}
		if err := RunAccept(ctx, out, acc); err != nil {
			return fmt.Errorf("auto-accept transfer %s: %w", instructionID, err)
		}
		emit(out, "transfer accepted", map[string]any{
			"transfer_instruction_id": instructionID,
			"receiver":                opts.To,
		})
	}
	return nil
}

func RunAccept(ctx context.Context, out io.Writer, opts AcceptOptions) error {
	if err := requireFields("transfer accept", "instance", opts.Instance,
		"transfer instruction id", opts.TransferInstructionID); err != nil {
		return err
	}
	opts.Party = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)
	if opts.Role == "" {
		opts.Role = DefaultRole
	}
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint == "" {
		emit(out, "transfer accept", map[string]any{
			"transfer_instruction_id": opts.TransferInstructionID,
		})
		return ErrNeedsV2LocalNet
	}
	if handled, err := runAcceptOnLedgerFn(ctx, out, opts); handled || err != nil {
		return err
	}
	return runAcceptOffLedgerFn(ctx, out, opts)
}

func RunBurn(ctx context.Context, out io.Writer, opts BurnOptions) error {
	if err := requireFields("burn", "instance", opts.Instance, "instrument", opts.Instrument,
		"holder party", opts.From, "amount", opts.Amount); err != nil {
		return err
	}
	opts.From = ResolveAlias(aliasMapForInstance(opts.Instance), opts.From)
	if err := validatePartyID("--from", opts.From); err != nil {
		return err
	}
	if err := validateAmount("burn", opts.Amount); err != nil {
		return err
	}
	if opts.Role == "" {
		opts.Role = DefaultRole
	}
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint == "" {
		emit(out, "burn", map[string]any{
			"instance": opts.Instance, "role": opts.Role,
			"endpoint": "", "from": opts.From, "amount": opts.Amount,
		})
		return unresolvedLedgerEndpoint(opts.Instance, opts.Role)
	}
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	if ref.Status == "on-ledger" {
		return runBurnLive(ctx, out, opts, ref)
	}
	emit(out, "burn", map[string]any{
		"instrument": ref, "from": opts.From, "amount": opts.Amount,
		"endpoint": opts.Endpoint, "role": opts.Role,
	})
	return ErrUnsupportedOnInstrument
}

func RunBalance(ctx context.Context, out io.Writer, opts BalanceOptions) ([]BalanceRow, bool, error) {
	if opts.Instance == "" {
		return nil, false, errors.New("instance is required")
	}
	opts.Party = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint != "" {
		return runBalanceLive(ctx, opts)
	}
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, false, fmt.Errorf("read instance state: %w", err)
	}
	rows := make([]BalanceRow, 0, len(state.Tokens))
	for _, t := range state.Tokens {
		if opts.Instrument != "" && opts.Instrument != t.Symbol && opts.Instrument != t.InstrumentID {
			continue
		}
		party := opts.Party
		if party == "" {
			party = t.IssuerParty
		}
		amount := "0"
		if party == t.IssuerParty {
			amount = t.InitialSupply
		}
		rows = append(rows, BalanceRow{
			InstrumentSymbol: t.Symbol,
			InstrumentID:     t.InstrumentID,
			Party:            party,
			Amount:           amount,
			Source:           SourceRegistry,
		})
	}
	return rows, false, nil
}

func BalanceSource(opts BalanceOptions) HoldingSource {
	if opts.Endpoint != "" || ResolveLedgerEndpoint(opts.Instance, opts.Role) != "" {
		return SourceLedger
	}
	return SourceRegistry
}

func runBalanceLive(ctx context.Context, opts BalanceOptions) ([]BalanceRow, bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, false, fmt.Errorf("read instance state: %w", err)
	}
	var wantAdmin, wantID string
	if opts.Instrument != "" {
		if ref, ok := state.Tokens[opts.Instrument]; ok {
			wantAdmin, wantID = ref.IssuerParty, ref.InstrumentID
		} else {
			wantID = opts.Instrument
		}
	}

	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedgerFn(ctx, conn)
	if err != nil {
		return nil, false, err
	}
	defer cleanup()

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("ledger end: %w", err)
	}

	var parties []string
	if opts.Party != "" {
		parties = []string{opts.Party}
	} else {
		// Canton's wildcard (FiltersForAnyParty) path needs a super-reader
		// / ParticipantAdmin claim the per-role tokens don't carry, so
		// enumerate the role's local parties and submit them in
		// FiltersByParty for per-party gating instead.
		discovered, err := localPartiesForRole(ctx, client, opts.Role)
		if err != nil {
			return nil, false, fmt.Errorf("discover local parties for role %q: %w", opts.Role, err)
		}
		parties = discovered
	}
	if len(parties) == 0 {
		// No parties → empty FiltersByParty (which Canton rejects), and
		// nothing to report anyway.
		return nil, false, nil
	}
	surfaces, err := discoverTokenSurfaces(ctx, client)
	if err != nil {
		return nil, false, err
	}
	if !surfaces.Any() {
		return nil, false, nil
	}
	req := ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    holdingInterfaceFilter(surfaces, parties),
	}
	stream, err := client.ActiveContracts(ctx, req)
	if err != nil {
		return nil, false, fmt.Errorf("ACS query: %w", err)
	}

	type bucketKey struct{ admin, id, party string }
	bucket := map[bucketKey]string{}

	var scanned int
	var truncated bool
	for item := range stream {
		if item.Err != nil {
			return nil, false, fmt.Errorf("ACS stream: %w", item.Err)
		}
		scanned++
		if scanned > maxHoldingsScan {
			truncated = true
			break
		}
		// ContractEntry is a oneof — only the ActiveContract branch carries
		// the CreatedEvent we need; IncompleteAssigned/Unassigned events
		// are mid-reassignment and don't affect the balance.
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok || entry.ActiveContract == nil {
			continue
		}
		hv, ok := extractBestHoldingView(entry.ActiveContract.GetCreatedEvent().GetInterfaceViews())
		if !ok || hv.Owner == "" {
			continue
		}
		if wantID != "" && hv.InstrumentID != wantID {
			continue
		}
		if wantAdmin != "" && hv.Admin != wantAdmin {
			continue
		}
		k := bucketKey{admin: hv.Admin, id: hv.InstrumentID, party: hv.Owner}
		sum, err := addDecimal(bucket[k], hv.Amount)
		if err != nil {
			return nil, false, fmt.Errorf("sum holding amounts: %w", err)
		}
		bucket[k] = sum
	}

	symByID := make(map[string]string, len(state.Tokens))
	for _, t := range state.Tokens {
		symByID[t.IssuerParty+"\x00"+t.InstrumentID] = t.Symbol
	}
	rows := make([]BalanceRow, 0, len(bucket))
	for k, amount := range bucket {
		rows = append(rows, BalanceRow{
			InstrumentSymbol: symByID[k.admin+"\x00"+k.id],
			InstrumentID:     k.id,
			Party:            k.party,
			Amount:           amount,
			Source:           SourceLedger,
		})
	}
	return rows, truncated, nil
}

var ErrSupplyCapExceeded = errors.New("mint exceeds the instrument's declared supply")

func enforceSupplyCap(ctx context.Context, opts MintOptions, ref registry.TokenRef) error {
	declared, ok := new(big.Rat).SetString(ref.InitialSupply)
	if !ok {
		return nil
	}
	want, ok := new(big.Rat).SetString(opts.Amount)
	if !ok {
		return nil
	}
	sum, err := RunInstrumentSummary(ctx, BalanceOptions{
		Instance: opts.Instance, Role: opts.Role, Insecure: opts.Insecure,
		Endpoint: opts.Endpoint, Instrument: ref.InstrumentID,
	})
	if err != nil {
		return nil
	}
	minted, ok := new(big.Rat).SetString(zeroIfEmpty(sum.TotalSupply))
	if !ok {
		return nil
	}

	after := new(big.Rat).Add(minted, want)
	if after.Cmp(declared) <= 0 {
		return nil
	}
	headroom := new(big.Rat).Sub(declared, minted)
	if headroom.Sign() < 0 {
		headroom.SetInt64(0)
	}
	return fmt.Errorf("%w: %s declares %s, %s already minted — at most %s more can be minted",
		ErrSupplyCapExceeded, ref.Symbol, trimDecimal(declared),
		trimDecimal(minted), trimDecimal(headroom))
}

func trimDecimal(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return strings.TrimRight(r.FloatString(10), "0")
}

func addDecimal(a, b string) (string, error) {
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	intA, fracA := splitDecimal(a)
	intB, fracB := splitDecimal(b)
	if len(fracA) < len(fracB) {
		fracA = fracA + strings.Repeat("0", len(fracB)-len(fracA))
	} else if len(fracB) < len(fracA) {
		fracB = fracB + strings.Repeat("0", len(fracA)-len(fracB))
	}
	scale := len(fracA)
	aBig := new(big.Int)
	if _, ok := aBig.SetString(intA+fracA, 10); !ok {
		return "", fmt.Errorf("invalid decimal %q", a)
	}
	bBig := new(big.Int)
	if _, ok := bBig.SetString(intB+fracB, 10); !ok {
		return "", fmt.Errorf("invalid decimal %q", b)
	}
	sum := new(big.Int).Add(aBig, bBig).String()
	if scale == 0 {
		return sum, nil
	}
	if len(sum) <= scale {
		sum = strings.Repeat("0", scale+1-len(sum)) + sum
	}
	return sum[:len(sum)-scale] + "." + sum[len(sum)-scale:], nil
}

func splitDecimal(s string) (intPart, fracPart string) {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func instrumentRefOrRaw(instance, ident string) registry.TokenRef {
	ref, err := resolveInstrument(instance, ident)
	if err != nil {
		return registry.TokenRef{InstrumentID: ident}
	}
	return ref
}

func resolveInstrument(instance, ident string) (registry.TokenRef, error) {
	if ident == "" {
		return registry.TokenRef{}, errors.New("--instrument is required")
	}
	state, err := registry.Read(instance)
	if err != nil {
		return registry.TokenRef{}, fmt.Errorf("read instance state: %w", err)
	}
	if t, ok := state.Tokens[ident]; ok {
		return t, nil
	}
	for _, t := range state.Tokens {
		if t.InstrumentID == ident {
			return t, nil
		}
	}
	return registry.TokenRef{}, fmt.Errorf(
		"no instrument %q on instance %q (run `localnet token create` first)",
		ident, instance)
}

func requireFields(verb string, nameValuePairs ...string) error {
	for i := 0; i+1 < len(nameValuePairs); i += 2 {
		if nameValuePairs[i+1] == "" {
			return fmt.Errorf("%s: %s is required", verb, nameValuePairs[i])
		}
	}
	return nil
}

func validateAmount(verb, amount string) error {
	if !looksLikeDecimal(amount) {
		return fmt.Errorf(
			"%s: --amount %q is not a valid decimal (digits and at most one '.', no sign or exponent)",
			verb, amount)
	}
	if isZeroDecimal(amount) {
		return fmt.Errorf("%s: --amount must be greater than zero", verb)
	}
	return nil
}

func isZeroDecimal(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '1' && s[i] <= '9' {
			return false
		}
	}
	return true
}

func emit(out io.Writer, verb string, payload map[string]any) {
	if out == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "%s: ", verb)
	enc := json.NewEncoder(out)
	_ = enc.Encode(payload)
}
