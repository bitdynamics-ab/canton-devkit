package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
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
	mux.HandleFunc("GET /api/tokens/{symbol}", handleTokenDetail)
	mux.HandleFunc("GET /api/tokens/{symbol}/holdings", handleTokenHoldings)
	mux.HandleFunc("POST /api/tokens", handleTokensCreate)
	mux.HandleFunc("POST /api/tokens/{symbol}/mint", handleTokenMint)
	mux.HandleFunc("POST /api/tokens/{symbol}/transfer", handleTokenTransfer)
	mux.HandleFunc("POST /api/tokens/transfers/{id}/accept", handleTokenAccept)
	mux.HandleFunc("POST /api/tokens/{symbol}/burn", handleTokenBurn)
}

// --- read paths ------------------------------------------------------

func handleTokensList(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
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
	rows, err := token.RunBalance(r.Context(), nil, token.BalanceOptions{
		Instance:   instance,
		Party:      r.URL.Query().Get("party"),
		Instrument: r.PathValue("symbol"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "balance", err)
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
	res, err := token.RunCreate(nil, token.CreateOptions{
		Instance:      instance,
		Name:          req.Name,
		Symbol:        req.Symbol,
		Decimals:      req.Decimals,
		InitialSupply: req.InitialSupply,
		Issuer:        req.Issuer,
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
	err = token.RunMint(r.Context(), nil, token.MintOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		To:         body.To,
		Amount:     body.Amount,
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
	err = token.RunTransfer(r.Context(), nil, token.TransferOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		From:       body.From,
		To:         body.To,
		Amount:     body.Amount,
		NoWait:     body.NoWait,
		Reason:     body.Reason,
	})
	mapTokenError(w, err, "transfer")
}

func handleTokenAccept(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	err = token.RunAccept(r.Context(), nil, token.AcceptOptions{
		Instance:              instance,
		TransferInstructionID: r.PathValue("id"),
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
	err = token.RunBurn(r.Context(), nil, token.BurnOptions{
		Instance:   instance,
		Instrument: r.PathValue("symbol"),
		From:       body.From,
		Amount:     body.Amount,
	})
	mapTokenError(w, err, "burn")
}

// --- helpers ---------------------------------------------------------

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
//   - nil                         → 204 No Content (idempotent success
//     for mutations that don't return a body)
//   - token.ErrNeedsV2LocalNet   → 412 Precondition Failed
//   - token.ErrSymbolInUse       → 409 Conflict
//   - other                      → 400 / 500 with the message
func mapTokenError(w http.ResponseWriter, err error, op string) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, token.ErrNeedsV2LocalNet):
		writeErrorWithCode(w, http.StatusPreconditionFailed,
			"NEEDS_V2_LOCALNET", err.Error())
	case errors.Is(err, token.ErrSymbolInUse):
		writeErrorWithCode(w, http.StatusConflict,
			"SYMBOL_IN_USE", err.Error())
	default:
		// Once the actions submit for real, failures arrive as gRPC
		// status errors — map their codes to the matching HTTP status
		// instead of flattening everything to 400. (Today the actions
		// stub at ErrNeedsV2LocalNet → 412, so this is forward-looking.)
		if s, ok := status.FromError(err); ok && s.Code() != codes.OK {
			switch s.Code() {
			case codes.NotFound:
				writeErrorWithCode(w, http.StatusNotFound, "NOT_FOUND", s.Message())
			case codes.PermissionDenied, codes.Unauthenticated:
				writeErrorWithCode(w, http.StatusForbidden, "PERMISSION_DENIED", s.Message())
			case codes.InvalidArgument:
				writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, s.Message())
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
		// correct for 5xx but unhelpful for a 400. Consistent with the
		// explicit 400s in the per-handler decode paths.
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
	}
}
