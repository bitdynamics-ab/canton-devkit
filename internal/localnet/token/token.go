// Package token implements the orchestration layer for `localnet token`
// — create, mint, transfer, burn, balance, transfer accept — that both
// the CLI (internal/cli/localnet/token) and the Web UI handler
// (internal/ui/handlers/tokens) call into. Keeps CLI ↔ Web UI parity
// automatic: one RunX function per verb, two thin wrappers.
package token

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// ErrSymbolInUse is returned by RunCreate when the instance's
// registry already records an instrument with the given symbol.
// Callers (CLI + HTTP handler) map this to ExitUserError / 409.
var ErrSymbolInUse = errors.New("symbol already in use on this instance")

// CreateOptions captures everything `token create` needs. The CLI (flags
// or interactive wizard) and the Web UI handler (JSON body) all populate
// it directly and call RunCreate — same code path either way.
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

	// Submit toggles actual ledger submission of the instrument
	// creation. Unused at this layer: RunCreate records the instrument
	// into the instance's registry.State.Tokens (Status "recorded") so
	// subsequent commands can resolve `--instrument <symbol>`; the
	// on-ledger path is driven by Endpoint below.
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
// isRoleName reports whether s is one of the token identity role names
// (app-user / app-provider / sv).
func isRoleName(s string) bool {
	for _, r := range Roles() {
		if s == r {
			return true
		}
	}
	return false
}

// firstLocalPartyForRole dials the ledger and returns the alphabetically
// first local party hosted for the role — the default issuer, matching how
// mint defaults --party and the Web UI's issuer picker offers the role's
// party. Returns "" when it can't resolve (offline, no local party) so the
// caller can fall back and let validation report a clear error.
func firstLocalPartyForRole(opts CreateOptions, role string) string {
	if role == "" {
		role = "app-user"
	}
	ctx := context.Background()
	client, cleanup, err := dialLedger(ctx, LedgerConn{
		Endpoint: opts.Endpoint,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     role,
	})
	if err != nil {
		return ""
	}
	defer cleanup()
	parties, err := localPartiesForRole(ctx, client, role)
	if err != nil || len(parties) == 0 {
		return ""
	}
	sort.Strings(parties)
	return parties[0]
}

func RunCreate(out io.Writer, opts CreateOptions) (*CreateResult, error) {
	// Resolve the issuer alias to its party id (as mint/transfer do).
	// Without this the raw alias flows through as the admin party and the
	// on-ledger TokenRules submit acts-as a non-existent party —
	// UNKNOWN_SUBMITTERS.
	opts.Issuer = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Issuer)

	// Default/resolve the issuer to a live party when it wasn't given as a
	// party id (no "::") — the CLI parallel to the Web UI's issuer picker,
	// which offers the acting role's own party. An empty issuer defaults to
	// the --role's party; a bare role name ("app-user") resolves to that
	// role's party. Only on the on-ledger path (Endpoint set); the
	// registry-only path keeps the issuer as typed. Best-effort: on failure
	// the issuer is left as-is and validateCreate reports it.
	if opts.Endpoint != "" && !strings.Contains(opts.Issuer, "::") {
		role := opts.Role
		if isRoleName(opts.Issuer) {
			role = opts.Issuer
		}
		if p := firstLocalPartyForRole(opts, role); p != "" {
			opts.Issuer = p
		}
	}

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
	// with the issuer admin), so transfers/balance resolve the instrument
	// by the same human symbol. The registry-only path keeps a generated
	// opaque id.
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
	// Guard against obvious garbage locally so the user gets a
	// friendly error rather than a confusing on-ledger one.
	// looksLikeDecimal rejects a lone "." / digit-less input, signs,
	// and exponents.
	if !looksLikeDecimal(opts.InitialSupply) {
		return fmt.Errorf("initial supply %q is not a valid decimal number",
			opts.InitialSupply)
	}
	// An instrument with zero initial supply is almost certainly a
	// typo; reject it here rather than persisting a token nobody can
	// mint against by default.
	if isZeroDecimal(opts.InitialSupply) {
		return fmt.Errorf("initial supply %q must be greater than zero",
			opts.InitialSupply)
	}
	// Daml's Numeric tops out at 38 significant digits and at most
	// `Decimals` fractional digits. Enforce both locally so an
	// oversized or over-precise supply fails with a clear message
	// instead of an opaque on-ledger rejection.
	intDigits, fracDigits := countDecimalDigits(opts.InitialSupply)
	if intDigits+fracDigits > damlNumericMaxDigits {
		return fmt.Errorf(
			"initial supply %q has %d significant digits, exceeding the %d-digit Numeric limit",
			opts.InitialSupply, intDigits+fracDigits, damlNumericMaxDigits)
	}
	if fracDigits > opts.Decimals {
		return fmt.Errorf(
			"initial supply %q has %d fractional digits, exceeding the declared decimals=%d",
			opts.InitialSupply, fracDigits, opts.Decimals)
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

// validatePartyID is the shared party-id guard used by the create
// issuer and the mint/transfer/burn party flags.
func validatePartyID(field, v string) error {
	if !partyIDPattern.MatchString(v) {
		return fmt.Errorf(
			"%s %q is not a valid party id (expected alphanumeric, optionally `hint::fingerprint`)",
			field, v)
	}
	return nil
}

// looksLikeDecimal accepts `123`, `123.456`, `0.5`; rejects signs and
// scientific notation for parity with Daml Decimal literal syntax. A
// digit-less input (e.g. a lone ".") is rejected: it renders as a bogus
// "." amount in the UI and breaks addDecimal's big.Int parse downstream.
func looksLikeDecimal(s string) bool {
	if s == "" {
		return false
	}
	sawDot := false
	sawDigit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			sawDigit = true
		case c == '.':
			if sawDot {
				return false
			}
			sawDot = true
		default:
			return false
		}
	}
	return sawDigit
}

// damlNumericMaxDigits is the max significant digits Daml's Numeric can
// represent (38 across integer + fractional parts). Enforced locally so
// an oversized supply fails with a friendly error, not an on-ledger one.
const damlNumericMaxDigits = 38

// countDecimalDigits returns the number of integer and fractional
// digits in a looksLikeDecimal-valid string (the dot is not counted).
// The two counts let validateCreate enforce both the total-precision
// bound and the "fractional digits must fit within Decimals" rule.
func countDecimalDigits(s string) (intDigits, fracDigits int) {
	intPart, fracPart := splitDecimal(s)
	return len(intPart), len(fracPart)
}

// newInstrumentID generates a hex-encoded 16-byte random InstrumentId.id.
// The V2 InstrumentId is `(admin, id)`; the id only has to be unique per
// admin, so a random 128-bit value is comfortable. Hex-encoded so it's
// URL- and shell-safe. crypto/rand for uniqueness, not secrecy.
func newInstrumentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return strings.ToLower(hex.EncodeToString(b[:])), nil
}
