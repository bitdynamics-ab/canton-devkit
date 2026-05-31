package token

import (
	"context"
	"fmt"
	"sort"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// The token "workspace" is the god-mode, ACS-derived view of an
// instance's token state (BIT-215 / BIT-219): every instrument, every
// party's balance, and the individual Holding contracts behind each
// balance. One ACS scan feeds three lenses:
//
//   - instruments    → on-chain instrument discovery (no state.Tokens seed)
//   - balance matrix → parties × instruments → summed amount
//   - party UTXOs    → a balance expanded into its Holding contracts
//
// All of it is read-only and backable today: we already query HoldingV2
// for balance; the workspace just keeps the per-contract rows instead of
// collapsing straight to a sum, and scans every locally-hosted party
// rather than one.

// HoldingContract is one HoldingV2 contract — the UTXO unit. A party's
// balance of an instrument is the sum of these.
type HoldingContract struct {
	ContractID   string `json:"contract_id"`
	Party        string `json:"party"`
	Admin        string `json:"admin"` // instrument admin/issuer party
	InstrumentID string `json:"instrument_id"`
	Amount       string `json:"amount"`
	Locked       bool   `json:"locked"`
}

// InstrumentRef is an on-chain-discovered instrument, enriched with any
// recorded metadata (name/symbol/decimals) from state.Tokens.
type InstrumentRef struct {
	Admin        string `json:"admin"`
	InstrumentID string `json:"instrument_id"`
	// Metadata (best-effort, from state.Tokens keyed by symbol==instrumentID).
	Name     string `json:"name,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Decimals int    `json:"decimals,omitempty"`
	Standard string `json:"standard,omitempty"` // "Splice Amulet" | "CIP-0112 v2"
	OnLedger bool   `json:"on_ledger"`          // discovered from the ACS
}

// Workspace is the full scanned state.
type Workspace struct {
	Parties     []string          `json:"parties"`
	Instruments []InstrumentRef   `json:"instruments"`
	Holdings    []HoldingContract `json:"holdings"` // every UTXO across all parties
}

// scanWorkspace dials the participant and scans HoldingV2 across every
// locally-hosted party (the parties this participant can read), keeping
// each Holding contract. Roles' JWTs carry CanReadAs the local parties,
// so a per-party filter returns them all without a super-reader claim.
func scanWorkspace(ctx context.Context, opts BalanceOptions) (*Workspace, error) {
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Scan only the parties the JWT is actually granted to read —
	// ResolveActAndReadParties is the authoritative set (the role's
	// auto-granted local party plus any explicitly granted, e.g. an
	// ad-hoc `bob`). Querying a party the token can't read returns
	// PermissionDenied for the whole stream. Full god-mode coverage
	// (read-as every hosted party) is BIT-215 #1's grant-on-up work.
	parties, err := client.ResolveActAndReadParties(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve readable parties: %w", err)
	}
	if len(parties) == 0 {
		// Fall back to the role's local party set (covers the first
		// scan before any grant has widened the readable set).
		parties, _ = localPartiesForRole(ctx, client, opts.Role)
	}
	if len(parties) == 0 {
		return &Workspace{}, nil
	}

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger end: %w", err)
	}
	stream, err := client.ActiveContracts(ctx, ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    holdingInterfaceFilterV2(parties),
	})
	if err != nil {
		return nil, fmt.Errorf("ACS query: %w", err)
	}

	var holdings []HoldingContract
	for item := range stream {
		if item.Err != nil {
			return nil, fmt.Errorf("ACS stream: %w", item.Err)
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		ce := entry.ActiveContract.GetCreatedEvent()
		if ce == nil {
			continue
		}
		for _, iv := range ce.GetInterfaceViews() {
			view, ok := extractHoldingViewV2(iv)
			if !ok {
				continue
			}
			holdings = append(holdings, HoldingContract{
				ContractID:   ce.GetContractId(),
				Party:        view.Owner,
				Admin:        view.Admin,
				InstrumentID: view.InstrumentID,
				Amount:       view.Amount,
				Locked:       view.Locked,
			})
		}
	}

	ws := &Workspace{
		Parties:     sortedUnique(partiesOf(holdings, parties)),
		Holdings:    holdings,
		Instruments: instrumentsFromHoldings(holdings, opts.Instance),
	}
	return ws, nil
}

// instrumentsFromHoldings collects the distinct (admin, instrumentId)
// pairs seen on the ledger and enriches each with recorded metadata.
func instrumentsFromHoldings(holdings []HoldingContract, instance string) []InstrumentRef {
	seen := map[string]*InstrumentRef{}
	order := []string{}
	for _, h := range holdings {
		key := h.Admin + "\x00" + h.InstrumentID
		if _, ok := seen[key]; !ok {
			seen[key] = &InstrumentRef{Admin: h.Admin, InstrumentID: h.InstrumentID, OnLedger: true}
			order = append(order, key)
		}
	}
	// Enrich from state.Tokens (keyed by symbol; our on-ledger create
	// uses symbol == instrumentId, so they line up).
	if state, err := registry.Read(instance); err == nil {
		for _, ref := range state.Tokens {
			for _, key := range order {
				ir := seen[key]
				if ir.InstrumentID == ref.InstrumentID || ir.InstrumentID == ref.Symbol {
					ir.Name = ref.Name
					ir.Symbol = ref.Symbol
					ir.Decimals = ref.Decimals
				}
			}
		}
	}
	out := make([]InstrumentRef, 0, len(order))
	for _, key := range order {
		ir := seen[key]
		if ir.Symbol == "" {
			ir.Symbol = ir.InstrumentID
		}
		ir.Standard = standardFor(ir.InstrumentID)
		out = append(out, *ir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// standardFor labels the token standard. Amulet is the V1-era Canton
// Coin (dual V1+V2); everything else we create is native CIP-0112 v2.
func standardFor(instrumentID string) string {
	if instrumentID == "Amulet" {
		return "Splice Amulet"
	}
	return "CIP-0112 v2"
}

// MatrixCell is one (party, instrument) summed amount.
type MatrixCell struct {
	Party        string `json:"party"`
	InstrumentID string `json:"instrument_id"`
	Amount       string `json:"amount"`
}

// BalanceMatrix pivots the workspace into party × instrument cells plus
// per-instrument totals.
type BalanceMatrix struct {
	Parties     []string        `json:"parties"`
	Instruments []InstrumentRef `json:"instruments"`
	Cells       []MatrixCell    `json:"cells"`  // sparse: only non-zero
	Totals      []MatrixCell    `json:"totals"` // per instrument (party="")
}

// buildMatrix sums holdings per (party, instrumentId).
func buildMatrix(ws *Workspace) *BalanceMatrix {
	type key struct{ party, inst string }
	sums := map[key]string{}
	totals := map[string]string{}
	for _, h := range ws.Holdings {
		k := key{h.Party, h.InstrumentID}
		// addDecimal only errors on malformed input; ACS amounts are
		// always well-formed Decimals, so a defensive skip is enough.
		if s, err := addDecimal(sums[k], h.Amount); err == nil {
			sums[k] = s
		}
		if s, err := addDecimal(totals[h.InstrumentID], h.Amount); err == nil {
			totals[h.InstrumentID] = s
		}
	}
	cells := make([]MatrixCell, 0, len(sums))
	for k, v := range sums {
		cells = append(cells, MatrixCell{Party: k.party, InstrumentID: k.inst, Amount: v})
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Party != cells[j].Party {
			return cells[i].Party < cells[j].Party
		}
		return cells[i].InstrumentID < cells[j].InstrumentID
	})
	tot := make([]MatrixCell, 0, len(totals))
	for inst, v := range totals {
		tot = append(tot, MatrixCell{InstrumentID: inst, Amount: v})
	}
	sort.Slice(tot, func(i, j int) bool { return tot[i].InstrumentID < tot[j].InstrumentID })
	return &BalanceMatrix{
		Parties:     ws.Parties,
		Instruments: ws.Instruments,
		Cells:       cells,
		Totals:      tot,
	}
}

// --- small helpers ---

func partiesOf(holdings []HoldingContract, base []string) []string {
	set := map[string]struct{}{}
	for _, p := range base {
		set[p] = struct{}{}
	}
	for _, h := range holdings {
		if h.Party != "" {
			set[h.Party] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	return out
}

func sortedUnique(in []string) []string {
	set := map[string]struct{}{}
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// RunBalanceMatrix scans the workspace and returns the party×instrument
// matrix. The live-ledger entrypoint both CLI and the HTTP handler call.
func RunBalanceMatrix(ctx context.Context, opts BalanceOptions) (*BalanceMatrix, error) {
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	ws, err := scanWorkspace(ctx, opts)
	if err != nil {
		return nil, err
	}
	return buildMatrix(ws), nil
}

// RunWorkspaceHoldings scans and returns every Holding contract (UTXO),
// optionally filtered by party and/or instrument. Powers the party-UTXO
// lens (expand a balance into its contracts).
func RunWorkspaceHoldings(ctx context.Context, opts BalanceOptions) ([]HoldingContract, error) {
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	ws, err := scanWorkspace(ctx, opts)
	if err != nil {
		return nil, err
	}
	out := make([]HoldingContract, 0, len(ws.Holdings))
	for _, h := range ws.Holdings {
		if opts.Party != "" && h.Party != opts.Party {
			continue
		}
		if opts.Instrument != "" && h.InstrumentID != opts.Instrument {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// RunInstruments scans the ACS and returns the discovered instruments
// (enriched with recorded metadata). On-chain discovery — no
// state.Tokens seed required, so Amulet + any minted token appear.
func RunInstruments(ctx context.Context, opts BalanceOptions) ([]InstrumentRef, error) {
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	ws, err := scanWorkspace(ctx, opts)
	if err != nil {
		return nil, err
	}
	return ws.Instruments, nil
}
