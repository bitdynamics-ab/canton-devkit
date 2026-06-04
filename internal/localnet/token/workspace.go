package token

import (
	"context"
	"fmt"
	"math/big"
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

// maxWorkspaceScan caps how many ACS contracts scanWorkspace will
// consume before declaring the result truncated. A real instance
// rarely holds more than a few thousand HoldingV2 contracts; the
// cap is a belt-and-braces guard against a runaway ledger pumping
// us into OOM via the unbounded gRPC stream.
const maxWorkspaceScan = 10_000

// Workspace is the full scanned state.
type Workspace struct {
	Parties     []string          `json:"parties"`
	Instruments []InstrumentRef   `json:"instruments"`
	Holdings    []HoldingContract `json:"holdings"` // every UTXO across all parties
	// Truncated is set when the ACS scan hit maxWorkspaceScan and
	// stopped early; the UI can render "showing N of many" rather
	// than silently misreporting the workspace.
	Truncated bool `json:"truncated,omitempty"`
}

// scanWorkspace dials the participant and scans HoldingV2 across every
// locally-hosted party (the parties this participant can read), keeping
// each Holding contract. Roles' JWTs carry CanReadAs the local parties,
// so a per-party filter returns them all without a super-reader claim.
func scanWorkspace(ctx context.Context, opts BalanceOptions) (*Workspace, error) {
	// Cancel the upstream stream pump on every return path so an
	// early break (cap, decode error) doesn't leak the goroutine for
	// the lifetime of the parent request context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedgerFn(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Scan the parties the JWT is granted to read. resolveReadableParties
	// first widens that set with CanReadAs for every registered party
	// alias (BIT-215 #1) so the god-mode matrix sees ALL aliased parties,
	// then re-resolves the authoritative granted set — a party that
	// couldn't be granted never enters the filter (querying it would
	// PermissionDenied the whole stream).
	parties, err := resolveReadableParties(ctx, client, opts.Instance, opts.Role)
	if err != nil {
		return nil, err
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
	var scanned int
	var truncated bool
	for item := range stream {
		if item.Err != nil {
			return nil, fmt.Errorf("ACS stream: %w", item.Err)
		}
		scanned++
		if scanned > maxWorkspaceScan {
			truncated = true
			break
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
		Truncated:   truncated,
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
	// Truncated propagates from the underlying Workspace so the UI
	// can surface "matrix shows N of many" rather than misleading
	// per-instrument totals.
	Truncated bool `json:"truncated,omitempty"`
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
		Truncated:   ws.Truncated,
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

// HolderRow is one party's stake in an instrument: summed balance, how
// many Holding contracts back it, and its share of total supply.
type HolderRow struct {
	Party         string `json:"party"`
	Balance       string `json:"balance"`
	ContractCount int    `json:"contract_count"`
	PctOfSupply   string `json:"pct_of_supply"` // e.g. "99.4"
}

// InstrumentSummary is the instrument-first KPI view (BIT-219 lens 1):
// total supply (= circulating, on a UTXO ledger), holder + contract
// counts, and the per-holder distribution.
type InstrumentSummary struct {
	InstrumentID  string      `json:"instrument_id"`
	Admin         string      `json:"admin"`
	TotalSupply   string      `json:"total_supply"`
	HolderCount   int         `json:"holder_count"`
	ContractCount int         `json:"contract_count"`
	Holders       []HolderRow `json:"holders"`
}

// RunInstrumentSummary scans the workspace and aggregates the holdings
// of one instrument (opts.Instrument) into the KPI + distribution view.
func RunInstrumentSummary(ctx context.Context, opts BalanceOptions) (*InstrumentSummary, error) {
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	ws, err := scanWorkspace(ctx, opts)
	if err != nil {
		return nil, err
	}
	return summarizeInstrument(ws, opts.Instrument), nil
}

// summarizeInstrument pivots a workspace scan into one instrument's
// supply + per-holder distribution. Pure — unit-testable.
func summarizeInstrument(ws *Workspace, instrumentID string) *InstrumentSummary {
	bal := map[string]string{} // party → summed balance
	cnt := map[string]int{}    // party → contract count
	var admin string
	total := ""
	order := []string{}
	for _, h := range ws.Holdings {
		if h.InstrumentID != instrumentID {
			continue
		}
		admin = h.Admin
		if _, seen := bal[h.Party]; !seen {
			order = append(order, h.Party)
		}
		if s, err := addDecimal(bal[h.Party], h.Amount); err == nil {
			bal[h.Party] = s
		}
		cnt[h.Party]++
		if s, err := addDecimal(total, h.Amount); err == nil {
			total = s
		}
	}
	totalF, _ := new(big.Float).SetString(zeroIfEmpty(total))
	holders := make([]HolderRow, 0, len(order))
	for _, p := range order {
		holders = append(holders, HolderRow{
			Party:         p,
			Balance:       bal[p],
			ContractCount: cnt[p],
			PctOfSupply:   pctOf(bal[p], totalF),
		})
	}
	// Descending by balance — biggest holder first.
	sort.Slice(holders, func(i, j int) bool {
		a, _ := new(big.Float).SetString(zeroIfEmpty(holders[i].Balance))
		b, _ := new(big.Float).SetString(zeroIfEmpty(holders[j].Balance))
		return a.Cmp(b) > 0
	})
	return &InstrumentSummary{
		InstrumentID:  instrumentID,
		Admin:         admin,
		TotalSupply:   zeroIfEmpty(total),
		HolderCount:   len(order),
		ContractCount: countContracts(ws, instrumentID),
		Holders:       holders,
	}
}

func countContracts(ws *Workspace, instrumentID string) int {
	n := 0
	for _, h := range ws.Holdings {
		if h.InstrumentID == instrumentID {
			n++
		}
	}
	return n
}

func zeroIfEmpty(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// pctOf returns balance/total*100 as a 1-decimal string, "0" when total
// is zero.
func pctOf(balance string, total *big.Float) string {
	if total == nil || total.Sign() == 0 {
		return "0"
	}
	b, ok := new(big.Float).SetString(zeroIfEmpty(balance))
	if !ok {
		return "0"
	}
	pct := new(big.Float).Quo(b, total)
	pct.Mul(pct, big.NewFloat(100))
	return pct.Text('f', 1)
}
