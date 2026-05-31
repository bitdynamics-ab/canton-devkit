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
// Tokens screen (BIT-140). All handlers delegate to
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
}

// --- read paths ------------------------------------------------------

func handleTokensList(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	// On-chain instrument discovery (BIT-219): when a live ledger
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
// (BIT-219 / BIT-215 #2) — the god-mode reconciliation view.
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

func handleTokenHoldings(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	// Discover the role's participant ledger port from the registry so
	// the UI hits the live V2 ACS, same shape as contracts.go. Empty
	// role defaults to app-user; absent port falls back to the registry
	// pseudo-balance path (used when the instance pre-dates port
	// capture). Auto-grant + per-party filter inside dialLedger /
	// runBalanceLive handle the V2 alpha permission gate transparently.
	role := r.URL.Query().Get("role")
	if role == "" {
		role = "app-user"
	}
	opts := token.BalanceOptions{
		Instance:   instance,
		Party:      r.URL.Query().Get("party"),
		Instrument: r.PathValue("symbol"),
		Role:       role,
		Insecure:   true, // LocalNet default
	}
	if state, err := registry.Read(instance); err == nil {
		if port, ok := state.Ports["participant_ledger_"+role]; ok && port > 0 {
			opts.Endpoint = "localhost:" + strconv.Itoa(port)
		}
	}
	// expand=contracts → return the individual Holding contracts (the
	// UTXO units) instead of the summed-per-party balance (BIT-219
	// party-UTXO lens). A party's balance is the sum of these.
	if r.URL.Query().Get("expand") == "contracts" && opts.Endpoint != "" {
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
	if truncated {
		// Surface the truncation so the UI can render a "showing N of
		// many" hint instead of silently misreporting the wallet.
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": types.SchemaVersion,
			"holdings":       rows,
			"truncated":      true,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": types.SchemaVersion,
		"holdings":       rows,
	})
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
		From   string `json:"from"`
		To     string `json:"to"`
		Amount string `json:"amount"`
		NoWait bool   `json:"no_wait"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	err = token.RunTransfer(r.Context(), nil, token.TransferOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		From:       body.From,
		To:         body.To,
		Amount:     body.Amount,
		NoWait:     body.NoWait,
		Reason:     body.Reason,
		// Live-submit: resolve the role's ledger endpoint so the
		// handler runs the real V2 transfer (registry-URL auto-derives
		// from the instance). Absent port → RunTransfer surfaces the
		// not-wired remediation, mapped to 412.
		Endpoint: liveLedgerEndpoint(instance, role),
		Role:     role,
		Insecure: true,
	})
	mapTokenError(w, err, "transfer")
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
// stub. Same port-discovery shape as handleTokenHoldings + contracts.go.
func liveLedgerEndpoint(instance, role string) string {
	state, err := registry.Read(instance)
	if err != nil {
		return ""
	}
	if port, ok := state.Ports["participant_ledger_"+role]; ok && port > 0 {
		return "localhost:" + strconv.Itoa(port)
	}
	return ""
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
