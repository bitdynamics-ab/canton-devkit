// Package token implements the orchestration layer for `localnet token`
// — create, mint, transfer, burn, balance, transfer accept — that both
// the CLI (internal/cli/localnet/token) and the Web UI handler
// (internal/ui/handlers/tokens) call into. Keeps CLI ↔ Web UI parity
// automatic: one RunX function per verb, two thin wrappers.
package token

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// ErrSymbolInUse is returned by RunCreate when the instance's
// registry already records an instrument with the given symbol.
// Callers (CLI + HTTP handler) map this to ExitUserError / 409.
var ErrSymbolInUse = errors.New("symbol already in use on this instance")

// CreateOptions captures everything `token create` needs.
//
// The non-interactive CLI path and the Web UI handler both populate
// this struct directly (from flags / JSON body); the interactive CLI
// wizard populates it from stdin prompts and then calls RunCreate
// with the populated struct — same code path either way.
type CreateOptions struct {
	// Instance is the registered LocalNet name; required.
	Instance string

	// Name / Symbol / Decimals / InitialSupply / Issuer are the wizard
	// inputs. All required.
	Name          string
	Symbol        string
	Decimals      int
	InitialSupply string
	Issuer        string

	// Submit toggles the actual ledger submission of the instrument
	// creation. Currently always false at this layer — the V2
	// instrument-creation submission lands when BIT-139 wires the
	// ledger client against the live V2 splice-test-token-v2
	// templates. For now RunCreate records the intent into the
	// instance's registry.State.Tokens so subsequent commands can
	// resolve `--instrument <symbol>` and the UI can render the
	// instrument card. Status field on the resulting TokenRef
	// reflects this: "recorded" until a real submission happens.
	Submit bool

	// Endpoint, when set (host:port), makes RunCreate create the
	// instrument on-ledger: it ensures a TokenRules contract exists
	// for the issuer party (admin) and records the instrument with
	// status "on-ledger". Empty Endpoint keeps the registry-only
	// "recorded" path. Role/Insecure mirror the other live actions.
	Endpoint string
	Role     string
	Insecure bool
}

// CreateResult is what RunCreate returns to the CLI and the HTTP
// handler. Both surfaces serialise it via the canonical
// registry.TokenRef shape (which mirrors api/types.TokenRef).
type CreateResult struct {
	TokenRef registry.TokenRef
}

// RunCreate validates the inputs, generates a deterministic
// instrument ID, persists a TokenRef into state.json under the
// per-instance flock, and returns the new entry. On a duplicate
// symbol it returns ErrSymbolInUse without mutating anything.
func RunCreate(out io.Writer, opts CreateOptions) (*CreateResult, error) {
	if err := validateCreate(opts); err != nil {
		return nil, err
	}

	release, err := registry.Lock(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("acquire instance lock: %w", err)
	}
	defer release()

	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("read instance state: %w", err)
	}

	if state.Tokens == nil {
		state.Tokens = make(map[string]registry.TokenRef)
	}
	if _, dup := state.Tokens[opts.Symbol]; dup {
		return nil, fmt.Errorf("%w: %q", ErrSymbolInUse, opts.Symbol)
	}

	// On-ledger create uses the symbol as the V2 InstrumentId.id (paired
	// with the issuer admin party), so transfers/balance resolve the
	// instrument by the same human symbol. The registry-only path keeps
	// a generated opaque id.
	instrumentID := opts.Symbol
	status := "on-ledger"
	if opts.Endpoint == "" {
		var err error
		if instrumentID, err = newInstrumentID(); err != nil {
			return nil, fmt.Errorf("generate instrument id: %w", err)
		}
		status = "recorded"
	} else {
		// Ensure the issuer's TokenRules contract exists on-ledger.
		// Idempotent: one TokenRules per admin anchors every instrument
		// that admin issues.
		if err := ensureTokenRules(opts); err != nil {
			return nil, err
		}
	}

	ref := registry.TokenRef{
		Name:          opts.Name,
		Symbol:        opts.Symbol,
		Decimals:      opts.Decimals,
		InitialSupply: opts.InitialSupply,
		IssuerParty:   opts.Issuer,
		InstrumentID:  instrumentID,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Status:        status,
	}
	state.Tokens[opts.Symbol] = ref
	if err := registry.Write(state); err != nil {
		return nil, fmt.Errorf("persist instance state: %w", err)
	}

	if out != nil {
		_, _ = fmt.Fprintf(out,
			"Recorded V2 instrument %q (symbol=%s, instrument_id=%s, issuer=%s).\n"+
				"Initial supply %s with %d decimals.\n",
			ref.Name, ref.Symbol, ref.InstrumentID, ref.IssuerParty,
			ref.InitialSupply, ref.Decimals)
		if opts.Endpoint != "" {
			_, _ = fmt.Fprintln(out,
				"Created on-ledger: a TokenRules contract anchors this instrument "+
					"for the issuer. `token mint --endpoint ...` can now mint supply.")
		} else {
			_, _ = fmt.Fprintln(out,
				"Note: instrument is recorded LOCALLY only — pass --endpoint to "+
					"create it on-ledger (requires the splice-test-token-v2 DAR "+
					"uploaded). Subsequent commands can resolve --instrument by symbol.")
		}
	}
	return &CreateResult{TokenRef: ref}, nil
}

// ListTokens reads the per-instance Tokens registry. Returns an empty
// slice (not nil) when no tokens have been recorded — the JSON output
// shape stays an array, not null.
func ListTokens(instance string) ([]registry.TokenRef, error) {
	state, err := registry.Read(instance)
	if err != nil {
		return nil, fmt.Errorf("read instance state: %w", err)
	}
	out := make([]registry.TokenRef, 0, len(state.Tokens))
	for _, t := range state.Tokens {
		out = append(out, t)
	}
	return out, nil
}

// ResolveBySymbol returns the TokenRef registered under the given
// symbol on the given instance, or a not-found error. Used by the
// mint/transfer/burn commands to map `--instrument RTK` to a full ref.
func ResolveBySymbol(instance, symbol string) (registry.TokenRef, error) {
	state, err := registry.Read(instance)
	if err != nil {
		return registry.TokenRef{}, err
	}
	if t, ok := state.Tokens[symbol]; ok {
		return t, nil
	}
	return registry.TokenRef{},
		fmt.Errorf("no instrument with symbol %q on instance %q "+
			"(use `localnet token create` first, or pass --instrument by full id)",
			symbol, instance)
}

// validateCreate centralises shape checks shared between the
// interactive wizard, the non-interactive CLI flags, and the Web UI
// handler's JSON body. All three render the returned error verbatim
// to the user.
func validateCreate(opts CreateOptions) error {
	if opts.Instance == "" {
		return errors.New("instance name is required")
	}
	if opts.Name == "" {
		return errors.New("instrument name is required")
	}
	sym := opts.Symbol
	if sym == "" {
		return errors.New("symbol is required")
	}
	if len(sym) > 16 {
		return fmt.Errorf("symbol %q exceeds 16 characters", sym)
	}
	// Symbol may include ASCII letters / digits / underscore so it
	// round-trips cleanly through path segments and shell args.
	for i := 0; i < len(sym); i++ {
		c := sym[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return fmt.Errorf("symbol %q contains disallowed character %q "+
				"(allowed: letters, digits, underscore)", sym, c)
		}
	}
	if opts.Decimals < 0 || opts.Decimals > 18 {
		return fmt.Errorf("decimals %d out of range [0..18]", opts.Decimals)
	}
	if opts.InitialSupply == "" {
		return errors.New("initial supply is required (decimal string)")
	}
	// Surface-level decimal validation — Daml-side accepts a
	// fixed-precision decimal; here we just guard against obvious
	// garbage so the user gets a friendly local error rather than a
	// confusing on-ledger one.
	if !looksLikeDecimal(opts.InitialSupply) {
		return fmt.Errorf("initial supply %q is not a valid decimal number",
			opts.InitialSupply)
	}
	if opts.Issuer == "" {
		return errors.New("issuer party is required")
	}
	if err := validatePartyID("issuer", opts.Issuer); err != nil {
		return err
	}
	return nil
}

// partyIDPattern is a light syntactic guard for a party id / hint: starts
// alphanumeric, then the party-id character set. Accepts BOTH a bare hint
// ("alice") and a fully-qualified id ("alice::1220ab…") — the ledger
// resolves which is which — but rejects whitespace, empties, and garbage
// that a bare non-empty check let through.
var partyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_#./-]*$`)

// validatePartyID is the shared party-id guard reused by the create
// issuer and (post-merge) the mint/transfer/burn party flags.
func validatePartyID(field, v string) error {
	if !partyIDPattern.MatchString(v) {
		return fmt.Errorf(
			"%s %q is not a valid party id (expected alphanumeric, optionally `hint::fingerprint`)",
			field, v)
	}
	return nil
}

// looksLikeDecimal accepts strings of the form `123`, `123.456`, or
// `0.5`. Reject leading + / scientific notation / sign for parity
// with Daml Decimal literal syntax.
func looksLikeDecimal(s string) bool {
	if s == "" {
		return false
	}
	sawDot := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			// ok
		case c == '.':
			if sawDot {
				return false
			}
			sawDot = true
		default:
			return false
		}
	}
	return true
}

// newInstrumentID generates a 16-byte random InstrumentId.id string,
// hex-encoded. The V2 InstrumentId is `(admin, id)` — the admin is
// the issuer party we already know; the id only has to be unique per
// admin, so a random 128-bit value is comfortable. Stored as the hex
// string so it's safe to put in URLs (instrument detail endpoint
// path) and shell args without escaping. Uses crypto/rand for the
// uniqueness guarantee, not for secrecy.
func newInstrumentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return strings.ToLower(hex.EncodeToString(b[:])), nil
}
