package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	regclient "github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// tokensBodyMax caps any /api/tokens request body; token bodies are
// small structured JSON.
const tokensBodyMax = 64 << 10 // 64 KiB

// MountTokens wires the /api/tokens HTTP surface for the Web UI Tokens
// screen. All handlers delegate to internal/localnet/token.RunX (the
// same functions the CLI calls), so CLI ↔ UI parity is automatic. The
// hub arg is reserved for future SSE live updates.
func MountTokens(mux *http.ServeMux, _ *stream.Hub) {
	mux.HandleFunc("GET /api/tokens", handleTokensList)
	mux.HandleFunc("GET /api/tokens/identity", handleTokenIdentity)
	mux.HandleFunc("GET /api/tokens/matrix", handleTokenMatrix)
	mux.HandleFunc("GET /api/tokens/{symbol}", handleTokenDetail)
	mux.HandleFunc("GET /api/tokens/{symbol}/summary", handleTokenSummary)
	mux.HandleFunc("GET /api/tokens/{symbol}/activity", handleTokenActivity)
	mux.HandleFunc("GET /api/tokens/{symbol}/holdings", handleTokenHoldings)
	// State-changing POSTs are wrapped with Idempotency-Key dedup (opt-in
	// on the header) so a client retry can't mint/transfer/burn twice.
	idem := newIdemStore()
	mux.HandleFunc("POST /api/tokens", idem.wrap(handleTokensCreate))
	mux.HandleFunc("POST /api/tokens/demo", idem.wrap(handleTokensDemo))
	mux.HandleFunc("POST /api/tokens/{symbol}/mint", idem.wrap(handleTokenMint))
	mux.HandleFunc("POST /api/tokens/{symbol}/transfer", idem.wrap(handleTokenTransfer))
	// Pending transfer offers: list is a GET (ACS scan of the
	// TransferInstruction interface); accept is an idempotent POST.
	mux.HandleFunc("GET /api/tokens/transfers", handlePendingTransfersList)
	mux.HandleFunc("POST /api/tokens/transfers/{id}/accept", idem.wrap(handleTokenAccept))
	mux.HandleFunc("POST /api/tokens/{symbol}/burn", idem.wrap(handleTokenBurn))
	mux.HandleFunc("POST /api/tokens/{symbol}/faucet", idem.wrap(handleTokenFaucet))
	// V2 DvP allocations: list is a GET (ACS scan); allocate + the
	// per-allocation actions are idempotent-wrapped POSTs.
	mux.HandleFunc("GET /api/tokens/allocations", handleAllocationsList)
	mux.HandleFunc("POST /api/tokens/{symbol}/allocate", idem.wrap(handleTokenAllocate))
	// Settlement (SettlementFactory_SettleBatch) is not yet functional on
	// LocalNet, so the settle action is intentionally not exposed. See
	// docs/changes-from-proposal.md.
	mux.HandleFunc("POST /api/tokens/allocations/{id}/withdraw", idem.wrap(handleAllocationWithdraw))
	mux.HandleFunc("POST /api/tokens/allocations/{id}/cancel", idem.wrap(handleAllocationCancel))
	// Party alias registry. Same RunPartyX functions the `token party` CLI calls.
	mux.HandleFunc("GET /api/parties", handlePartiesList)
	mux.HandleFunc("POST /api/parties", handlePartiesCreate)
	mux.HandleFunc("DELETE /api/parties/{alias}", handlePartiesRemove)
}

// aliasMap returns partyID → alias for an instance's registered parties,
// attached to scan responses so the UI can label party ids without a
// second round-trip.
func aliasMap(instance string) map[string]string {
	out := map[string]string{}
	state, err := registry.Read(instance)
	if err != nil {
		return out
	}
	for _, ref := range state.Parties {
		if ref.PartyID != "" {
			out[ref.PartyID] = ref.Alias
		}
	}
	return out
}

func handlePartiesList(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	// Endpoint enables lazy-seeding the role parties; absent endpoint
	// still lists whatever's recorded.
	parties, err := token.RunPartyList(r.Context(), token.PartyOptions{
		Instance: instance, Role: role, Insecure: true,
		Endpoint: liveLedgerEndpoint(instance, role),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list parties", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": types.SchemaVersion,
		"parties":        parties,
	})
}

func handlePartiesCreate(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var req struct {
		Alias string `json:"alias"`
		Role  string `json:"role"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := req.Role
	if role == "" {
		role = token.DefaultRole
	}
	ep := liveLedgerEndpoint(instance, role)
	if ep == "" {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "PARTICIPANT_PORT_NOT_RECORDED",
			"no live ledger endpoint for instance "+instance+" — restart it so ports are captured")
		return
	}
	ref, err := token.RunPartyNew(r.Context(), token.PartyOptions{
		Instance: instance, Alias: req.Alias, Role: role, Insecure: true, Endpoint: ep,
	})
	if err != nil {
		mapTokenError(w, err, "party new")
		return
	}
	writeJSON(w, http.StatusCreated, ref)
}

func handlePartiesRemove(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	if err := token.RunPartyRemove(instance, r.PathValue("alias")); err != nil {
		mapTokenError(w, err, "party rm")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- read paths ------------------------------------------------------

// handleTokenIdentity serves the act-as identity picker (token.Roles()
// plus the request's role), backing the Web UI switcher and the CLI
// `token identity` verb. Read-only, no ledger dial, so it answers even
// when the instance is down.
func handleTokenIdentity(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.TokenIdentityResponse{
		SchemaVersion:  types.SchemaVersion,
		Instance:       instance,
		AvailableRoles: token.Roles(),
		CurrentRole:    roleFromQuery(r),
	})
}

func handleTokensList(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	// Shared live-then-recorded decision + wire shape (token.ListInstruments,
	// also behind `token ls`): with a live endpoint, on-chain ACS discovery
	// (so Amulet and any minted token appear without a state.Tokens seed);
	// otherwise the recorded list. liveLedgerEndpoint stays the resolution
	// seam so offline handler tests force the recorded path deterministically.
	role := roleFromQuery(r)
	endpoint := liveLedgerEndpoint(instance, role)
	resp, err := token.ListInstruments(r.Context(), token.BalanceOptions{
		Instance: instance, Role: role, Insecure: true,
		Endpoint: endpoint,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tokens", err)
		return
	}
	// Echo the endpoint contract so the Web UI payload matches `token ls
	// --format json` (empty endpoint = recorded/offline fallback).
	resp.ResolvedEndpoint = types.ResolvedEndpoint{Endpoint: endpoint, Role: role}
	writeJSON(w, http.StatusOK, resp)
}

// handleTokenMatrix returns the party × instrument balance matrix.
func handleTokenMatrix(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	ep := liveLedgerEndpoint(instance, role)
	if ep == "" {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "PARTICIPANT_PORT_NOT_RECORDED",
			"no live ledger endpoint for instance "+instance+" — restart it so ports are captured")
		return
	}
	matrix, err := token.RunBalanceMatrix(r.Context(), token.BalanceOptions{
		Instance: instance, Role: role, Insecure: true, Endpoint: ep,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "balance matrix", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": types.SchemaVersion,
		"matrix":         matrix,
		"aliases":        aliasMap(instance),
	})
}

func handleTokenDetail(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	sym := r.PathValue("symbol")
	ref, err := token.ResolveBySymbol(instance, sym)
	if err != nil {
		writeErrorWithCode(w, http.StatusNotFound, "INSTRUMENT_NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ref)
}

// handleTokenSummary returns the instrument KPI view: total supply,
// holder + holding-contract counts, and the per-holder distribution.
// ACS-derived, same live endpoint as the matrix.
func handleTokenSummary(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	ep := liveLedgerEndpoint(instance, role)
	if ep == "" {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "PARTICIPANT_PORT_NOT_RECORDED",
			"no live ledger endpoint for instance "+instance+" — restart it so ports are captured")
		return
	}
	// Resolve the symbol to its instrument id so the scan's per-holding
	// instrumentId filter lines up (for native tokens id == symbol).
	sym := r.PathValue("symbol")
	instrumentID := sym
	if ref, rerr := token.ResolveBySymbol(instance, sym); rerr == nil && ref.InstrumentID != "" {
		instrumentID = ref.InstrumentID
	}
	summary, err := token.RunInstrumentSummary(r.Context(), token.BalanceOptions{
		Instance: instance, Role: role, Insecure: true, Endpoint: ep,
		Instrument: instrumentID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "instrument summary", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": types.SchemaVersion,
		"summary":        summary,
		"aliases":        aliasMap(instance),
	})
}

// handleTokenActivity reconstructs an instrument's transfer/mint/burn
// history from the ledger transaction stream (HoldingV2 create/archive
// events). ?limit caps the result (default 50).
func handleTokenActivity(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	ep := liveLedgerEndpoint(instance, role)
	if ep == "" {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "PARTICIPANT_PORT_NOT_RECORDED",
			"no live ledger endpoint for instance "+instance+" — restart it so ports are captured")
		return
	}
	sym := r.PathValue("symbol")
	instrumentID := sym
	if ref, rerr := token.ResolveBySymbol(instance, sym); rerr == nil && ref.InstrumentID != "" {
		instrumentID = ref.InstrumentID
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	res, err := token.RunActivityResult(r.Context(), token.BalanceOptions{
		Instance: instance, Role: role, Insecure: true, Endpoint: ep,
		Instrument: instrumentID, Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token activity", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": types.SchemaVersion,
		"events":         res.Events,
		"truncated":      res.Truncated,
		"aliases":        aliasMap(instance),
		// Shared resolution metadata (endpoint contract): which
		// participant host:port and act-as role produced this feed.
		"endpoint": ep,
		"role":     role,
	})
}

func handleTokenHoldings(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	// Resolve the endpoint once and thread it via opts.Endpoint, rather
	// than having the expand guard, RunBalance, and BalanceSource each
	// re-resolve it (≈4 registry reads/request). Present endpoint → live
	// ACS path (SourceLedger); absent → registry pseudo-balance
	// (SourceRegistry), which the response source tags so the UI can flag it.
	role := roleFromQuery(r)
	endpoint := token.ResolveLedgerEndpoint(instance, role)
	opts := token.BalanceOptions{
		Instance:   instance,
		Party:      r.URL.Query().Get("party"),
		Instrument: r.PathValue("symbol"),
		Role:       role,
		Insecure:   true,     // LocalNet default
		Endpoint:   endpoint, // resolved once above; "" → registry fallback
	}
	// expand=contracts → return the individual Holding contracts (UTXO
	// units) instead of the summed-per-party balance. Requires a live endpoint.
	if r.URL.Query().Get("expand") == "contracts" && endpoint != "" {
		contracts, cerr := token.RunWorkspaceHoldings(r.Context(), opts)
		if cerr != nil {
			mapTokenError(w, cerr, "holdings")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": types.SchemaVersion,
			"contracts":      contracts,
		})
		return
	}
	rows, truncated, err := token.RunBalance(r.Context(), nil, opts)
	if err != nil {
		mapTokenError(w, err, "balance")
		return
	}
	// Tag whether these are live on-ledger balances or the registry
	// pseudo-balance fallback so the UI can flag the latter. Keyed on
	// endpoint availability (not reachability), exactly as RunBalance branches.
	source := token.SourceRegistry
	if endpoint != "" {
		source = token.SourceLedger
	}
	writeJSON(w, http.StatusOK, types.TokenHoldingsResponse{
		SchemaVersion:    types.SchemaVersion,
		ResolvedEndpoint: types.ResolvedEndpoint{Endpoint: endpoint, Role: role},
		Source:           types.HoldingSource(source),
		Holdings:         toHoldings(rows),
		Truncated:        truncated,
	})
}

// toHoldings converts token.BalanceRow into the shared
// api/types.TokenHolding wire shape (identical fields/tags). The
// conversion keeps the package boundary one-directional (handlers →
// api/types, localnet/token → neither).
func toHoldings(rows []token.BalanceRow) []types.TokenHolding {
	out := make([]types.TokenHolding, 0, len(rows))
	for _, r := range rows {
		out = append(out, types.TokenHolding{
			InstrumentSymbol: r.InstrumentSymbol,
			InstrumentID:     r.InstrumentID,
			Party:            r.Party,
			Amount:           r.Amount,
			Source:           types.HoldingSource(r.Source),
		})
	}
	return out
}

// --- write paths -----------------------------------------------------

func handleTokensCreate(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var req types.TokenCreateRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	// Thread Endpoint+Role so the UI's create path matches the CLI: a
	// captured participant port submits live, otherwise RunCreate falls
	// back to the registry-only stub.
	role := roleFromQuery(r)
	res, err := runTokenCreate(token.CreateOptions{
		Instance:      instance,
		Name:          req.Name,
		Symbol:        req.Symbol,
		Decimals:      req.Decimals,
		InitialSupply: req.InitialSupply,
		Issuer:        req.Issuer,
		Endpoint:      liveLedgerEndpoint(instance, role),
		Role:          role,
		Insecure:      true,
	})
	if err != nil {
		mapTokenError(w, err, "create")
		return
	}
	writeJSON(w, http.StatusCreated, res.Response())
}

// handleTokensDemo provisions a live demo token in one call (issuer →
// V2 instrument → mint → faucet a holder) — the server side of the
// "Launch demo token" button and the CLI `token demo` verb, both via
// token.RunDemo. The body is optional (defaults: symbol DEMO, supply
// 1,000,000, decimals 6). The supply is always minted to a holder party,
// so the demo token is transferable. Requires a live V2 endpoint; without
// one RunDemo returns ErrNeedsV2LocalNet → 412.
func handleTokensDemo(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var req struct {
		Symbol        string `json:"symbol"`
		InitialSupply string `json:"initial_supply"`
		Decimals      int    `json:"decimals"`
	}
	if err := decodeJSON(r.Body, &req); err != nil && !errors.Is(err, io.EOF) {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	res, err := runTokenDemo(r.Context(), nil, token.DemoOptions{
		Instance:      instance,
		Role:          role,
		Endpoint:      liveLedgerEndpoint(instance, role),
		Insecure:      true,
		Symbol:        req.Symbol,
		InitialSupply: req.InitialSupply,
		Decimals:      req.Decimals,
	})
	if err != nil {
		mapTokenError(w, err, "demo")
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func handleTokenMint(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var body struct {
		To     string `json:"to"`
		Amount string `json:"amount"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	err = token.RunMint(r.Context(), nil, token.MintOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		To:         body.To,
		Amount:     body.Amount,
		// Live mint for on-ledger test-token instruments; Amulet /
		// registry-only instruments take the unsupported path.
		Endpoint: liveLedgerEndpoint(instance, role),
		Role:     role,
		Insecure: true,
	})
	mapTokenError(w, err, "mint")
}

func handleTokenTransfer(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var body struct {
		From       string `json:"from"`
		To         string `json:"to"`
		Amount     string `json:"amount"`
		NoWait     bool   `json:"no_wait"`
		AutoAccept bool   `json:"auto_accept"`
		Atomic     bool   `json:"atomic"`
		Reason     string `json:"reason"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	opts := token.TransferOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		From:       body.From,
		To:         body.To,
		Amount:     body.Amount,
		Role:       role,
		Insecure:   true,
	}
	// plan=1 → dry-run coin selection (consume/change preview), no submit.
	if r.URL.Query().Get("plan") == "1" {
		plan, perr := token.RunTransferPlan(r.Context(), opts)
		if perr != nil {
			mapTokenError(w, perr, "transfer plan")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": types.SchemaVersion,
			"plan":           plan,
		})
		return
	}
	// Live-submit resolves the endpoint after the preview branch so preview
	// validation never touches registry/endpoint state first.
	opts.Endpoint = liveLedgerEndpoint(instance, role)
	opts.NoWait = body.NoWait
	opts.AutoAccept = body.AutoAccept
	// Atomic batches transfer+accept into one BatchingUtilityV2
	// transaction. EXPERIMENTAL — not yet supported on current Splice
	// (RunTransfer returns an actionable error and nothing commits);
	// sequential is the default.
	opts.Atomic = body.Atomic
	opts.Reason = body.Reason
	// Capture RunTransfer's emitted events to return the created
	// TransferInstruction id — without it the two-step offer→accept flow
	// is unusable from the UI (the receiver can't learn the Accept id).
	var buf bytes.Buffer
	if err := runTokenTransfer(r.Context(), &buf, opts); err != nil {
		mapTokenError(w, err, "transfer")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version":          types.SchemaVersion,
		"transfer_instruction_id": extractEmittedID(buf.String()),
		// settled → the UI skips the Accept step (auto-accept chained it,
		// or a Direct/self transfer needed none).
		"settled": opts.AutoAccept,
	})
}

// extractEmittedID scans RunTransfer's emitted "<verb>: {json}" lines
// for a transfer_instruction_id. Returns "" when absent (e.g. a
// Direct/self transfer that settled with no pending instruction).
func extractEmittedID(emitted string) string {
	for _, line := range strings.Split(emitted, "\n") {
		brace := strings.Index(line, "{")
		if brace < 0 {
			continue
		}
		var payload struct {
			TransferInstructionID string `json:"transfer_instruction_id"`
		}
		if json.Unmarshal([]byte(line[brace:]), &payload) == nil && payload.TransferInstructionID != "" {
			return payload.TransferInstructionID
		}
	}
	return ""
}

// handleTokenFaucet funds a party from a well-known source, auto-accepted.
// POST /api/tokens/{symbol}/faucet {to, amount, source?}.
func handleTokenFaucet(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var body struct {
		To     string `json:"to"`
		Amount string `json:"amount"`
		Source string `json:"source"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	ep := liveLedgerEndpoint(instance, role)
	if ep == "" {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "PARTICIPANT_PORT_NOT_RECORDED",
			"no live ledger endpoint for instance "+instance)
		return
	}
	mapTokenError(w, token.RunFaucet(r.Context(), nil, token.FaucetOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		To:         body.To,
		Amount:     body.Amount,
		Source:     body.Source,
		Role:       role,
		Insecure:   true,
		Endpoint:   ep,
	}), "faucet")
}

func handleTokenAccept(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	err = runTokenAccept(r.Context(), token.AcceptOptions{
		Instance:              instance,
		TransferInstructionID: r.PathValue("id"),
		// Party targets the receiver on a multi-party participant: the
		// Accept controller is transfer.receiver, so without it RunAccept
		// falls back to the JWT's first granted party (usually wrong).
		// Mirrors the CLI's `token transfer accept --party`.
		Party:    r.URL.Query().Get("party"),
		Endpoint: liveLedgerEndpoint(instance, role),
		Role:     role,
		Insecure: true,
	})
	mapTokenError(w, err, "accept")
}

func handleTokenBurn(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var body struct {
		From   string `json:"from"`
		Amount string `json:"amount"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	err = token.RunBurn(r.Context(), nil, token.BurnOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		From:       body.From,
		Amount:     body.Amount,
		Endpoint:   liveLedgerEndpoint(instance, role),
		Role:       role,
		Insecure:   true,
	})
	mapTokenError(w, err, "burn")
}

// --- helpers ---------------------------------------------------------

// runTokenCreate is the indirection point for handleTokensCreate. Tests
// override it to assert the handler threads Endpoint+Role through.
var runTokenCreate = func(opts token.CreateOptions) (*token.CreateResult, error) {
	return token.RunCreate(nil, opts)
}

// runTokenTransfer / runTokenAccept are indirection points so tests can
// assert the wiring (surfaced instruction id, ?party= threading)
// without a live ledger.
var runTokenTransfer = func(ctx context.Context, out io.Writer, opts token.TransferOptions) error {
	return token.RunTransfer(ctx, out, opts)
}

var runTokenAccept = func(ctx context.Context, opts token.AcceptOptions) error {
	return token.RunAccept(ctx, nil, opts)
}

// runTokenDemo is the indirection point for handleTokensDemo. Tests
// override it to assert the handler threads instance/role/endpoint +
// seed flag through to RunDemo.
var runTokenDemo = token.RunDemo

// roleFromQuery returns the `?role=` value, defaulting to
// token.DefaultRole (app-provider). Explorer/DAR handlers keep their
// own app-user defaults and do not use this helper.
func roleFromQuery(r *http.Request) string {
	role := r.URL.Query().Get("role")
	if role == "" {
		role = token.DefaultRole
	}
	return role
}

// liveLedgerEndpoint resolves the role's participant ledger gRPC
// endpoint from the instance's recorded ports (empty when uncaptured →
// callers fall back to the not-wired stub). Delegates to
// token.ResolveLedgerEndpoint so the CLI and this handler resolve the
// same participant. A package var (like runTokenCreate) so registry-only
// handler tests can force the offline path deterministically.
var liveLedgerEndpoint = func(instance, role string) string {
	return token.ResolveLedgerEndpoint(instance, role)
}

// instanceFromQuery validates `?instance=` and protects the per-name
// path-traversal surface via registry.ValidateName.
func instanceFromQuery(r *http.Request) (string, error) {
	instance := r.URL.Query().Get("instance")
	if instance == "" {
		return "", errors.New("missing required query param: instance")
	}
	if err := registry.ValidateName(instance); err != nil {
		return "", err
	}
	return instance, nil
}

// decodeJSON consumes the request body with a strict body cap and
// DisallowUnknownFields so a typo'd field reaches the user as a 400
// rather than being silently dropped.
func decodeJSON(r io.ReadCloser, into any) error {
	defer func() { _ = r.Close() }()
	dec := json.NewDecoder(io.LimitReader(r, tokensBodyMax))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	return nil
}

// partyIDFingerprint matches the fingerprint half of a Daml party id
// (`<hint>::<fingerprint>`) — the enumeration-sensitive part to mask.
var partyIDFingerprint = regexp.MustCompile(`([A-Za-z0-9._-]+::)[A-Za-z0-9]{8,}`)

// sanitize400 masks party-id fingerprints in a user-facing 400: an
// upstream gRPC InvalidArgument message may embed a fully-qualified party
// id, so drop the fingerprint (keep the readable hint) to prevent
// enumeration.
func sanitize400(msg string) string {
	return partyIDFingerprint.ReplaceAllString(msg, "$1…")
}

// mapTokenError converts an orchestration-layer error into the
// matching HTTP status. Keeps every handler's failure-mapping
// identical so an ErrXxx → status mapping change only touches this
// one function.
//
//   - nil                              → 204 No Content (idempotent
//     success for mutations that don't return a body)
//   - token.ErrNeedsV2LocalNet         → 412 Precondition Failed
//   - token.ErrUnresolvedLedgerEndpoint → 503 PARTICIPANT_PORT_NOT_RECORDED
//   - token.ErrUnsupportedOnInstrument → 422 Unprocessable Entity
//   - token.ErrSupplyCapExceeded       → 422 Unprocessable Entity
//   - token.ErrSymbolInUse             → 409 Conflict
//   - other                            → 400 / 500 with the message
func mapTokenError(w http.ResponseWriter, err error, op string) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, token.ErrTokenDARUnavailable):
		// Example DAR not published for this Splice version: actionable
		// 412 (message names the remedy) rather than a raw upstream 404.
		writeErrorWithCode(w, http.StatusPreconditionFailed,
			"TEST_TOKEN_DAR_UNAVAILABLE", err.Error())
	case errors.Is(err, token.ErrNeedsV2LocalNet):
		writeErrorWithCode(w, http.StatusPreconditionFailed,
			"NEEDS_V2_LOCALNET", err.Error())
	case errors.Is(err, token.ErrUnresolvedLedgerEndpoint):
		writeErrorWithCode(w, http.StatusServiceUnavailable,
			"PARTICIPANT_PORT_NOT_RECORDED", err.Error())
	case errors.Is(err, token.ErrUnsupportedOnInstrument):
		writeErrorWithCode(w, http.StatusUnprocessableEntity,
			"UNSUPPORTED_ON_INSTRUMENT", err.Error())
	case errors.Is(err, token.ErrSupplyCapExceeded):
		writeErrorWithCode(w, http.StatusUnprocessableEntity,
			"SUPPLY_CAP_EXCEEDED", err.Error())
	case errors.Is(err, token.ErrSymbolInUse):
		writeErrorWithCode(w, http.StatusConflict,
			"SYMBOL_IN_USE", err.Error())
	case errors.Is(err, token.ErrAliasInvalid):
		writeErrorWithCode(w, http.StatusBadRequest,
			"ALIAS_INVALID", err.Error())
	case errors.Is(err, token.ErrAliasInUse):
		writeErrorWithCode(w, http.StatusConflict,
			"ALIAS_IN_USE", err.Error())
	case errors.Is(err, token.ErrAliasUnknown):
		writeErrorWithCode(w, http.StatusNotFound,
			"ALIAS_UNKNOWN", err.Error())
	default:
		// Off-ledger token-registry 4xx: preserve the upstream status
		// (422 INSUFFICIENT_FUNDS stays 422) with a short sanitized reason
		// — never the raw body, which can embed party-ids / URLs.
		var apiErr *regclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			reason := registryErrorReason(apiErr)
			writeErrorWithCode(w, apiErr.StatusCode,
				codeForStatus(apiErr.StatusCode), reason)
			return
		}
		// Live submits fail as gRPC status errors — map their codes to
		// the matching HTTP status instead of flattening to 400.
		if s, ok := status.FromError(err); ok && s.Code() != codes.OK {
			switch s.Code() {
			case codes.NotFound:
				writeErrorWithCode(w, http.StatusNotFound, "NOT_FOUND", s.Message())
			case codes.PermissionDenied, codes.Unauthenticated:
				writeErrorWithCode(w, http.StatusForbidden, "PERMISSION_DENIED", s.Message())
			case codes.InvalidArgument:
				// Upstream message may embed a party id — mask the fingerprint.
				writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, sanitize400(s.Message()))
			case codes.Unavailable, codes.DeadlineExceeded:
				// Participant unreachable/slow — redact per 5xx contract, log the cause.
				writeError(w, http.StatusServiceUnavailable, op, err)
			default:
				// Internal / Unknown — a server-side failure; redact like any 5xx.
				writeError(w, http.StatusBadGateway, op, err)
			}
			return
		}
		// Non-gRPC orchestration error: user-actionable (bad amount,
		// unknown party). Surface the cause — writeError would redact it
		// to the op name, unhelpful for a 400.
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
	}
}

// registryErrorReason extracts a short, safe reason from a
// token-registry APIError. Splice bodies are usually small JSON like
// `{"code":"INSUFFICIENT_FUNDS","message":"..."}`; surface the code, else
// a sanitized capped snippet. Never the full body (can leak party-ids / URLs).
func registryErrorReason(e *regclient.APIError) string {
	var probe struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(e.Body), &probe); err == nil {
		if probe.Code != "" {
			return probe.Code
		}
		if probe.Message != "" {
			return sanitize400(truncate(probe.Message, 200))
		}
	}
	return sanitize400(truncate(e.Body, 200))
}

// truncate caps a string at n runes with an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
