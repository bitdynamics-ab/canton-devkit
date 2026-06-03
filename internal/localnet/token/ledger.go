package token

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// LedgerConn captures everything a live ledger call needs: the gRPC
// endpoint and the Bearer JWT the participant accepts.
//
// Resolved by the CLI / Web UI from --endpoint + --token (or via
// state.Endpoints once the participant gRPC port surfacing follow-up
// lands; tracked in BIT-136's "endpoint auto-discovery" item).
type LedgerConn struct {
	Endpoint string // host:port, e.g. "localhost:61169"
	Token    string // Bearer JWT; empty allowed when the participant runs unauthenticated
	Insecure bool   // plaintext gRPC (LocalNet default)

	// Instance + Role let dialLedger resolve a per-role JWT
	// automatically when Token is empty: it tries the registry's
	// captured credentials first, then falls back to issuing a
	// fresh JWT via splice.SignToken against the cached project's
	// env files. Either path produces a Daml user-token with the
	// per-role audience the participant expects.
	Instance string
	Role     string // "sv" / "app-provider" / "app-user"
}

// dialLedger opens a ledger client against the given endpoint. Mirrors
// the helper in internal/cli/localnet/contracts.go but lives here so
// the action layer (which the HTTP handler also calls) can dial without
// pulling cli/localnet into the import graph. Caller MUST defer the
// returned cleanup.
//
// Token resolution order (the BIT-139 follow-up that drove this):
//  1. conn.Token explicit (`--token` flag wins).
//  2. registry.State.Credentials[Role] — populated by `localnet up`'s
//     captureCredentials when the alpha boot is clean. The CLI mints
//     against this via `localnet creds --role <role> --format raw`.
//  3. splice.SignToken fallback — issues a fresh user-token from the
//     project's env/<role>-auth-on.env files. Same shape as path #2;
//     used when creds-capture races / fails (the V2 alpha is wobbly
//     on cold boots and step (2) may be empty even after up succeeds).
//  4. error pointing at `localnet creds --raw` for the user to override.
func dialLedger(ctx context.Context, conn LedgerConn) (*ledger.Client, func(), error) {
	if conn.Endpoint == "" {
		return nil, func() {}, fmt.Errorf("ledger endpoint is required (host:port)")
	}
	token, err := resolveLedgerToken(conn)
	if err != nil {
		return nil, func() {}, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	opts := ledger.DialOptions{
		Endpoint: conn.Endpoint,
		// PlainText defaults to true (LocalNet-first). We surface
		// `Insecure` as a knob in our struct for symmetry with the
		// other clients, but the underlying client's PlainText is
		// the same gate.
		PlainText: conn.Insecure || !strings.HasPrefix(conn.Endpoint, "https://"),
	}
	if token != "" {
		opts.Token = ledger.StaticToken(token)
	}
	client, err := ledger.Dial(dialCtx, opts)
	if err != nil {
		return nil, func() {}, fmt.Errorf("dial ledger %s: %w", conn.Endpoint, err)
	}
	return client, func() { _ = client.Close() }, nil
}

// jwtPattern matches a compact JWS (three base64url segments, header
// starting with the conventional `eyJ`). Used by resolveLedgerToken to
// scrub any bearer token that might otherwise ride out in an error
// string toward the user's terminal or logs.
var jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// redactJWTs replaces any JWT-shaped substring with a fixed marker.
// Defence-in-depth: errors on the token-resolution path wrap file/role
// context today (not the token itself — SignToken returns the token only
// on success), but a future change to splice.SignToken or
// LoadCredentialInputs could embed credential material in an error. This
// guard ensures a bearer token can never reach the user via an error.
func redactJWTs(err error) error {
	if err == nil {
		return nil
	}
	masked := jwtPattern.ReplaceAllString(err.Error(), "[redacted-jwt]")
	if masked == err.Error() {
		return err // nothing matched — preserve the original error (and its wrapping)
	}
	return errors.New(masked)
}

// resolveLedgerToken implements the four-step token resolution order
// documented on dialLedger. Returns "" when no token is needed
// (participant runs without auth) — the caller treats empty as "no
// auth header" rather than an error. Any returned error is passed
// through redactJWTs so a bearer token can never leak via the error path.
func resolveLedgerToken(conn LedgerConn) (string, error) {
	tok, err := resolveLedgerTokenRaw(conn)
	return tok, redactJWTs(err)
}

func resolveLedgerTokenRaw(conn LedgerConn) (string, error) {
	if conn.Token != "" {
		return conn.Token, nil
	}
	if conn.Instance == "" {
		// No instance + no token: legitimate for an unauthenticated
		// participant or a hand-injected ledger. The dial proceeds
		// with no Authorization header; the participant errors with
		// 401 if it wanted one and the caller surfaces that as-is.
		return "", nil
	}
	state, err := registry.Read(conn.Instance)
	if err != nil {
		return "", fmt.Errorf("read instance state: %w", err)
	}

	role := conn.Role
	if role == "" {
		role = string(splice.RoleAppUser)
	}

	// Path #2: captured credentials from RunUp's captureCredentials.
	if c, ok := state.Credentials[role]; ok && c.JWT != "" {
		return c.JWT, nil
	}

	// Path #3: fall back to signing a fresh token from the project's
	// env files. This is the "creds were not captured / lost" path
	// the V2 alpha hits when onboarding interrupts up's tail steps.
	inputs, err := splice.LoadCredentialInputs(state.ProjectDir)
	if err != nil {
		return "", fmt.Errorf(
			"no captured credentials for role %q and could not load env files (%w). "+
				"Run `canton-devkit localnet creds --name %s --role %s --format raw` "+
				"to confirm token issuance, or pass --token explicitly",
			role, err, conn.Instance, role)
	}
	for _, in := range inputs {
		if string(in.Role) == role {
			tok, signErr := splice.SignToken(in)
			if signErr != nil {
				return "", fmt.Errorf("sign token for role %q: %w", role, signErr)
			}
			return tok, nil
		}
	}
	return "", fmt.Errorf(
		"no captured credentials AND no env entry for role %q on instance %q. "+
			"Run `canton-devkit localnet creds --name %s --role %s --format raw` "+
			"to confirm what's available, or pass --token explicitly",
		role, conn.Instance, conn.Instance, role)
}

// holdingInterfaceFilterV2 builds the EventFormat the participant
// accepts for "stream every contract that implements HoldingV2 and
// give me the parsed HoldingViewV2 record on each event".
//
// Parties:
//
//	nil/empty → wildcard (FiltersForAnyParty). The participant gates
//	            this by the Bearer JWT's claims — under LocalNet's
//	            `unsafe` dev secret with a per-role token the wildcard
//	            yields every party visible to that role.
//	non-empty → per-party filter; only the listed parties' holdings
//	            show up.
//
// The interface filter sets IncludeInterfaceView=true so the
// participant ships the HoldingViewV2 record alongside the contract
// id — no second round-trip to fetch the payload.
func holdingInterfaceFilterV2(parties []string) *lapiv2.EventFormat {
	pkg, mod, entity := parseInterfaceID(HoldingInterfaceV2)
	filter := &lapiv2.Filters{
		Cumulative: []*lapiv2.CumulativeFilter{{
			IdentifierFilter: &lapiv2.CumulativeFilter_InterfaceFilter{
				InterfaceFilter: &lapiv2.InterfaceFilter{
					InterfaceId: &lapiv2.Identifier{
						PackageId:  pkg,
						ModuleName: mod,
						EntityName: entity,
					},
					IncludeInterfaceView:    true,
					IncludeCreatedEventBlob: false,
				},
			},
		}},
	}
	if len(parties) == 0 {
		return &lapiv2.EventFormat{
			FiltersForAnyParty: filter,
			Verbose:            true,
		}
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = filter
	}
	return &lapiv2.EventFormat{
		FiltersByParty: byParty,
		Verbose:        true,
	}
}

// parseInterfaceID splits the upstream `#package-name:Module:Entity`
// form into the Identifier shape the Ledger API takes. The `#`-prefix
// is the package-name reference that the participant resolves to
// whichever vetted package satisfies the name at exercise time —
// keeps us robust against the V2 alpha's weekly snapshot rotation.
func parseInterfaceID(qual string) (pkg, module, entity string) {
	q := strings.TrimPrefix(qual, "#")
	parts := strings.Split(q, ":")
	switch len(parts) {
	case 3:
		return "#" + parts[0], parts[1], parts[2]
	case 2:
		return "", parts[0], parts[1]
	default:
		return "", "", ""
	}
}

// holdingViewV2 is the structured form of HoldingViewV2 we extract
// from a participant's InterfaceView Record. We carry just the fields
// the balance / Web UI need — adding more is a one-liner once a
// downstream consumer asks for them.
type holdingViewV2 struct {
	Owner        string // view.account.owner (Optional Party)
	InstrumentID string // view.instrumentId.id
	Admin        string // view.instrumentId.admin (the V2 InstrumentId admin = the issuer party)
	Amount       string // view.amount as a Decimal string (we don't round on the wire)
}

// extractHoldingViewV2 walks a participant InterfaceView Record and
// pulls out the four fields balance needs. Returns ok=false when the
// view is unparseable — caller then skips that contract rather than
// failing the whole ACS scan, since one badly-shaped view shouldn't
// break the entire wallet display.
//
// The Daml record shape this targets (per HoldingV2.daml):
//
//	HoldingView { account, instrumentId, amount, lock?, meta }
//	  Account { owner: Optional Party, provider: Optional Party, id: Text }
//	  InstrumentId { admin: Party, id: Text }
func extractHoldingViewV2(view *lapiv2.InterfaceView) (holdingViewV2, bool) {
	if view == nil || view.ViewValue == nil {
		return holdingViewV2{}, false
	}
	fields := view.ViewValue.Fields
	out := holdingViewV2{}
	for _, f := range fields {
		switch f.Label {
		case "account":
			rec := recordOf(f.Value)
			if rec == nil {
				return out, false
			}
			for _, af := range rec.Fields {
				if af.Label == "owner" {
					out.Owner = optionalPartyOf(af.Value)
				}
			}
		case "instrumentId":
			rec := recordOf(f.Value)
			if rec == nil {
				return out, false
			}
			for _, ifld := range rec.Fields {
				switch ifld.Label {
				case "admin":
					out.Admin = partyOf(ifld.Value)
				case "id":
					out.InstrumentID = textOf(ifld.Value)
				}
			}
		case "amount":
			out.Amount = numericOf(f.Value)
		}
	}
	if out.InstrumentID == "" || out.Amount == "" {
		return out, false
	}
	return out, true
}

// --- tiny Value walkers, focused on the fields we need ---

func recordOf(v *lapiv2.Value) *lapiv2.Record {
	if v == nil {
		return nil
	}
	if r, ok := v.Sum.(*lapiv2.Value_Record); ok {
		return r.Record
	}
	return nil
}

func partyOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if p, ok := v.Sum.(*lapiv2.Value_Party); ok {
		return p.Party
	}
	return ""
}

func optionalPartyOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if o, ok := v.Sum.(*lapiv2.Value_Optional); ok && o.Optional != nil && o.Optional.Value != nil {
		return partyOf(o.Optional.Value)
	}
	return ""
}

func textOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if t, ok := v.Sum.(*lapiv2.Value_Text); ok {
		return t.Text
	}
	return ""
}

func numericOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if n, ok := v.Sum.(*lapiv2.Value_Numeric); ok {
		return n.Numeric
	}
	return ""
}
