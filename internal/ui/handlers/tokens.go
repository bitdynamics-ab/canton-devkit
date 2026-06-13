package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	regclient "github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// tokensBodyMax caps any /api/tokens request body. Token bodies are
// small structured JSON — multi-MiB requests are always a mistake.
const tokensBodyMax = 64 << 10 // 64 KiB

// MountTokens wires the /api/tokens HTTP surface for the Web UI
// Tokens screen. All handlers delegate to
// internal/localnet/token.RunX — the same functions the CLI
// subcommands call — so CLI ↔ UI parity is automatic.
//
// Routes:
//
//	GET    /api/tokens                          ?instance=...  list instruments
//	GET    /api/tokens/{symbol}                 ?instance=...  detail (symbol or raw id)
//	GET    /api/tokens/{symbol}/holdings        ?instance=... [&party=]  per-party balances
//	POST   /api/tokens                          create instrument (body: TokenCreateRequest)
//	POST   /api/tokens/{symbol}/mint            mint to a recipient
//	POST   /api/tokens/{symbol}/transfer        sender-initiated transfer
//	POST   /api/tokens/transfers/{id}/accept    receiver-side accept
//	POST   /api/tokens/{symbol}/burn            burn a holding
//
// The hub argument is reserved for future SSE-backed live updates
// (the proposed `tokens:<instance>` topic); accepted now so callers
// don't have to refactor their MountTokens call when that lands.
func MountTokens(mux *http.ServeMux, _ *stream.Hub) {
	mux.HandleFunc("GET /api/tokens", handleTokensList)
	mux.HandleFunc("GET /api/tokens/matrix", handleTokenMatrix)
	mux.HandleFunc("GET /api/tokens/{symbol}", handleTokenDetail)
	mux.HandleFunc("GET /api/tokens/{symbol}/summary", handleTokenSummary)
	mux.HandleFunc("GET /api/tokens/{symbol}/activity", handleTokenActivity)
	mux.HandleFunc("GET /api/tokens/{symbol}/holdings", handleTokenHoldings)
	// State-changing POSTs are wrapped with Idempotency-Key dedup so a
	// client retry can't mint/transfer/burn twice (opt-in: only requests
	// carrying the header are deduplicated).
	idem := newIdemStore()
	mux.HandleFunc("POST /api/tokens", idem.wrap(handleTokensCreate))
	mux.HandleFunc("POST /api/tokens/{symbol}/mint", idem.wrap(handleTokenMint))
	mux.HandleFunc("POST /api/tokens/{symbol}/transfer", idem.wrap(handleTokenTransfer))
	mux.HandleFunc("POST /api/tokens/transfers/{id}/accept", idem.wrap(handleTokenAccept))
	mux.HandleFunc("POST /api/tokens/{symbol}/burn", idem.wrap(handleTokenBurn))
	mux.HandleFunc("POST /api/tokens/{symbol}/faucet", idem.wrap(handleTokenFaucet))
	// Party alias registry — the workspace's god-mode
	// party manager. Same RunPartyX functions the `token party` CLI calls.
	mux.HandleFunc("GET /api/parties", handlePartiesList)
	mux.HandleFunc("POST /api/parties", handlePartiesCreate)
	mux.HandleFunc("DELETE /api/parties/{alias}", handlePartiesRemove)
}

// aliasMap returns partyID → alias for an instance's registered parties,
// attached to scan responses so the UI can label party ids without a
// second round-trip. Empty map for an unknown / alias-less instance.
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
		role = "app-user"
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

func handleTokensList(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	// On-chain instrument discovery: when a live ledger
	// endpoint is available, list instruments seen in the ACS — so
	// Amulet and any minted token appear without a state.Tokens seed.
	// Falls back to the registry-recorded list when no endpoint (the
	// instance pre-dates port capture, or the ledger is unreachable).
	role := roleFromQuery(r)
	if ep := liveLedgerEndpoint(instance, role); ep != "" {
		instruments, derr := token.RunInstruments(r.Context(), token.BalanceOptions{
			Instance: instance, Role: role, Insecure: true, Endpoint: ep,
		})
		if derr == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"schema_version": types.SchemaVersion,
				"instruments":    instruments,
			})
			return
		}
		// Discovery failed (e.g. ledger momentarily unreachable) — fall
		// through to the recorded list rather than erroring the screen.
	}
	refs, err := token.ListTokens(instance)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tokens", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": types.SchemaVersion,
		"tokens":         refs,
	})
}

// handleTokenMatrix returns the party × instrument balance matrix
// — the god-mode reconciliation view.
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

// handleTokenSummary returns the instrument-first KPI view: total
// supply, holder + holding-contract counts, and the
// per-holder distribution with share-of-supply. ACS-derived, same live
// endpoint as the matrix.
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
	// Resolve the symbol to its on-ledger instrument id so the scan's
	// per-holding instrumentId filter lines up (the workspace keys on
	// instrument_id, which for our native tokens == symbol).
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
// history from the ledger transaction stream (Activity tab). No
// off-ledger transfer-events registry needed — derived from HoldingV2
// create/archive events. ?limit caps the result (default 50).
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
	})
}

func handleTokenHoldings(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	// RunBalance auto-discovers the role's participant ledger port from
	// the registry (via token.ResolveLedgerEndpoint) and hits the live
	// V2 ACS when it's present; an absent port falls back to the
	// registry pseudo-balance path (instance down / pre-port-capture).
	// Either way the response is tagged with its source (#63) so the UI
	// can flag the fallback. Auto-grant + per-party filter inside
	// dialLedger / runBalanceLive handle the V2 alpha permission gate
	// transparently. Empty role defaults to app-user.
	role := roleFromQuery(r)
	opts := token.BalanceOptions{
		Instance:   instance,
		Party:      r.URL.Query().Get("party"),
		Instrument: r.PathValue("symbol"),
		Role:       role,
		Insecure:   true, // LocalNet default
		// Endpoint is resolved by RunBalance via the shared
		// token.ResolveLedgerEndpoint when left empty — the same
		// participant the CLI's `token balance` auto-discovers.
	}
	// expand=contracts → return the individual Holding contracts
	// (the UTXO units) instead of the summed-per-party balance.
	// A party's balance is the sum of these. Requires a live endpoint.
	if r.URL.Query().Get("expand") == "contracts" && token.ResolveLedgerEndpoint(instance, role) != "" {
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
	// #63: tell the client whether these are live on-ledger balances or
	// the registry pseudo-balance fallback, so the UI can flag the
	// latter instead of presenting fabricated rows as real holdings.
	// Both the response-level Source and each row's Source are set —
	// the rows carry it from RunBalance; the response-level value lets
	// the UI render one disclaimer banner without scanning every row.
	writeJSON(w, http.StatusOK, types.TokenHoldingsResponse{
		SchemaVersion: types.SchemaVersion,
		Source:        types.HoldingSource(token.BalanceSource(opts)),
		Holdings:      toHoldings(rows),
		Truncated:     truncated,
	})
}

// toHoldings converts the neutral token.BalanceRow slice into the
// shared api/types.TokenHolding wire shape. The two structs mirror
// each other field-for-field (same JSON tags); the conversion exists
// so the package boundary stays one-directional (handlers → api/types,
// localnet/token → neither) while the emitted bytes are identical.
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
	// Thread Endpoint+Role through to RunCreate so the UI's
	// create-on-ledger path matches the CLI: an instance with a captured
	// participant port submits live, otherwise RunCreate falls back to
	// the registry-only stub. Same role/endpoint discovery shape as the
	// mint / transfer handlers below.
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
	writeJSON(w, http.StatusCreated, res.TokenRef)
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
		// Live mint for on-ledger test-token instruments. Amulet /
		// registry-only instruments still take the unsupported path.
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
		Endpoint:   liveLedgerEndpoint(instance, role),
	}
	// plan=1 → dry-run coin selection (consume/change preview), no submit.
	if r.URL.Query().Get("plan") == "1" {
		if opts.Endpoint == "" {
			writeErrorWithCode(w, http.StatusServiceUnavailable, "PARTICIPANT_PORT_NOT_RECORDED",
				"no live ledger endpoint for instance "+instance)
			return
		}
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
	// Live-submit: opts already carries the resolved endpoint/role.
	opts.NoWait = body.NoWait
	opts.AutoAccept = body.AutoAccept
	opts.Reason = body.Reason
	mapTokenError(w, token.RunTransfer(r.Context(), nil, opts), "transfer")
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
	err = token.RunAccept(r.Context(), nil, token.AcceptOptions{
		Instance:              instance,
		TransferInstructionID: r.PathValue("id"),
		Endpoint:              liveLedgerEndpoint(instance, role),
		Role:                  role,
		Insecure:              true,
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
// override it to assert that the handler is wiring Endpoint+Role through
// to the orchestration layer (the CLI gets them from flags; the UI must
// derive them from the instance registry + request role).
var runTokenCreate = func(opts token.CreateOptions) (*token.CreateResult, error) {
	return token.RunCreate(nil, opts)
}

// roleFromQuery returns the `?role=` value, defaulting to app-user.
func roleFromQuery(r *http.Request) string {
	role := r.URL.Query().Get("role")
	if role == "" {
		role = "app-user"
	}
	return role
}

// liveLedgerEndpoint resolves the role's participant ledger gRPC
// endpoint (host:port) from the instance's recorded ports. Empty when
// the port wasn't captured — callers then fall back to the not-wired
// stub. Delegates to the neutral token.ResolveLedgerEndpoint so the
// CLI's `token balance` auto-discovery and this handler resolve the
// same participant from the same `participant_ledger_<role>` key.
func liveLedgerEndpoint(instance, role string) string {
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

// mapTokenError converts an orchestration-layer error into the
// matching HTTP status. Keeps every handler's failure-mapping
// identical so a future ErrXxx → status mapping change only touches
// this one function.
//
//   - nil                              → 204 No Content (idempotent
//     success for mutations that don't return a body)
//   - token.ErrNeedsV2LocalNet         → 412 Precondition Failed
//   - token.ErrUnsupportedOnInstrument → 422 Unprocessable Entity
//   - token.ErrSymbolInUse             → 409 Conflict
//   - other                            → 400 / 500 with the message
//
// partyIDFingerprint matches the fingerprint half of a fully-qualified
// Daml party id (`<hint>::<fingerprint>`). The hint is human-readable;
// the fingerprint is the unique, enumeration-sensitive identifier.
var partyIDFingerprint = regexp.MustCompile(`([A-Za-z0-9._-]+::)[A-Za-z0-9]{8,}`)

// sanitize400 masks party-id fingerprints in a user-facing 400 message.
// Locally-constructed 400s are safe, but a gRPC InvalidArgument message
// is built upstream and could embed a fully-qualified party id; keep the
// readable hint, drop the fingerprint so an error body can't be used to
// enumerate party ids.
func sanitize400(msg string) string {
	return partyIDFingerprint.ReplaceAllString(msg, "$1…")
}

func mapTokenError(w http.ResponseWriter, err error, op string) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, token.ErrNeedsV2LocalNet):
		writeErrorWithCode(w, http.StatusPreconditionFailed,
			"NEEDS_V2_LOCALNET", err.Error())
	case errors.Is(err, token.ErrUnsupportedOnInstrument):
		writeErrorWithCode(w, http.StatusUnprocessableEntity,
			"UNSUPPORTED_ON_INSTRUMENT", err.Error())
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
		// Off-ledger token-registry 4xx: surface the upstream status
		// (so a 422 INSUFFICIENT_FUNDS stays a 422) and ship a short
		// sanitized reason — never the raw 4 KiB body, which can embed
		// party-ids / contract-ids / URLs.
		var apiErr *regclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			reason := registryErrorReason(apiErr)
			writeErrorWithCode(w, apiErr.StatusCode,
				codeForStatus(apiErr.StatusCode), reason)
			return
		}
		// Once the actions submit for real, failures arrive as gRPC
		// status errors — map their codes to the matching HTTP status
		// instead of flattening everything to 400.
		if s, ok := status.FromError(err); ok && s.Code() != codes.OK {
			switch s.Code() {
			case codes.NotFound:
				writeErrorWithCode(w, http.StatusNotFound, "NOT_FOUND", s.Message())
			case codes.PermissionDenied, codes.Unauthenticated:
				writeErrorWithCode(w, http.StatusForbidden, "PERMISSION_DENIED", s.Message())
			case codes.InvalidArgument:
				// s.Message() comes from upstream and may embed a
				// fully-qualified party id; mask the fingerprint before it
				// reaches the client.
				writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, sanitize400(s.Message()))
			case codes.Unavailable, codes.DeadlineExceeded:
				// Upstream participant unreachable / slow — redact the
				// detail (5xx contract) and log the cause.
				writeError(w, http.StatusServiceUnavailable, op, err)
			default:
				// Internal / Unknown / etc. — a genuine server-side
				// failure, not a bad request. Redact like any 5xx.
				writeError(w, http.StatusBadGateway, op, err)
			}
			return
		}
		// Non-gRPC orchestration error: user-actionable (bad amount,
		// unknown party, malformed instrument id). Surface the cause —
		// `writeError` would redact it to just the op name, which is
		// correct for 5xx but unhelpful for a 400.
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
	}
}

// registryErrorReason extracts a short, safe reason string from a
// token-registry APIError. Splice error bodies are usually small JSON
// of the form `{"code":"INSUFFICIENT_FUNDS","message":"..."}`; we try
// to surface that code (very high signal, low risk) and otherwise fall
// back to a sanitized snippet of the body capped to keep the response
// small. Never returns the full 4 KiB body — that can leak party-ids,
// contract-ids, and registry URLs.
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
