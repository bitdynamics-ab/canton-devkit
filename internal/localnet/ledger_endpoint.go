package localnet

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// Ledger endpoint + JWT resolution shared by the CLI Explorer
// commands (`contracts ls/watch`, `tx ls/replay`) and the token
// action layer. Before this, only the Web UI handlers
// (internal/ui/handlers/contracts.go resolveLedgerForRole) and the
// token package resolved the participant ledger port + per-role JWT
// from the registry — the CLI Explorer demanded an explicit
// --endpoint, so every shipped skill example (`dpm localnet
// contracts watch --name dev`) failed with a misleading "not yet
// exposed" error. This helper closes that CLI ↔ Web UI parity gap:
// it lives in the neutral internal/localnet package so both surfaces
// share one resolution path (registry → participant_ledger_<role>
// port → captured JWT → SignToken fallback) instead of drifting.

// LedgerEndpoint is the resolved (endpoint, token) pair a ledger
// client needs to dial a participant for a given instance + role.
// Token is empty only when the participant runs unauthenticated.
type LedgerEndpoint struct {
	Endpoint string // host:port, e.g. "localhost:2901"
	Token    string // Bearer JWT; empty allowed for unauthenticated participants
	Role     string // resolved role (defaulted to app-user when caller passed "")
	Instance string // resolved instance name (after default-instance lookup)
}

// ResolveLedgerEndpoint resolves the participant ledger gRPC endpoint
// and a per-role JWT for the named instance, mirroring what the Web
// UI's resolveLedgerForRole does. Resolution order:
//
//  1. instance — if empty, fall back to the only registered instance
//     (errors when zero or more than one is registered, so the
//     ambiguity is surfaced rather than guessed).
//  2. role — defaults to "app-user" when empty (same default the UI
//     and `dar` commands use).
//  3. endpoint — state.Ports["participant_ledger_"+role], captured at
//     `up`/`restart` by CaptureCantonPorts. Missing port → a clear
//     "restart to capture ports" error.
//  4. token — captured state.Credentials[role].JWT first, then a
//     fresh splice.SignToken from the cached project's env files
//     (the same fallback the token action layer uses when creds
//     capture raced on a cold V2 boot).
//
// Returns an error suitable for surfacing to the user (ExitUserError
// at the call site). Any token-resolution error is scrubbed of
// JWT-shaped material before returning.
func ResolveLedgerEndpoint(instance, role string) (LedgerEndpoint, error) {
	name, err := resolveInstanceName(instance)
	if err != nil {
		return LedgerEndpoint{}, err
	}
	if role == "" {
		role = string(splice.RoleAppUser)
	}

	state, err := registry.Read(name)
	if err == registry.ErrNotFound {
		return LedgerEndpoint{}, fmt.Errorf("instance %q not registered (run `localnet up` first)", name)
	}
	if err != nil {
		return LedgerEndpoint{}, fmt.Errorf("read instance state: %w", err)
	}

	portKey := "participant_ledger_" + role
	port, ok := state.Ports[portKey]
	if !ok || port == 0 {
		// Same failure shape as the Web UI's PARTICIPANT_PORT_NOT_RECORDED:
		// the instance pre-dates port capture (BIT-190) or this Splice
		// version doesn't expose the role. Point at the same remedy.
		return LedgerEndpoint{}, fmt.Errorf(
			"instance %q has no recorded participant ledger port for role %q — "+
				"restart it (`localnet down --name %s --keep-data` then `localnet up --name %s`) "+
				"so the new up flow captures Canton API ports, or pass --endpoint host:port",
			name, role, name, name)
	}

	token, err := resolveLedgerJWT(state, role)
	if err != nil {
		return LedgerEndpoint{}, err
	}

	return LedgerEndpoint{
		Endpoint: "localhost:" + strconv.Itoa(port),
		Token:    token,
		Role:     role,
		Instance: name,
	}, nil
}

// resolveInstanceName returns the explicit instance when set,
// otherwise the single registered instance. It errors (rather than
// guessing) when the registry holds zero or several instances — the
// `--name` help text promises "default: the only registered
// instance", and silently picking one of several would read the
// wrong ledger.
func resolveInstanceName(instance string) (string, error) {
	if instance != "" {
		return instance, nil
	}
	idx, err := registry.ReadIndex()
	if err != nil {
		return "", fmt.Errorf("list instances: %w", err)
	}
	switch len(idx.Entries) {
	case 0:
		return "", fmt.Errorf("no LocalNet instances registered — run `localnet up` or pass --name")
	case 1:
		return idx.Entries[0].Name, nil
	default:
		names := make([]string, 0, len(idx.Entries))
		for _, e := range idx.Entries {
			names = append(names, e.Name)
		}
		return "", fmt.Errorf("multiple instances registered (%v) — pass --name to choose one", names)
	}
}

// resolveLedgerJWT mirrors the token action layer's two captured-then-
// signed fallback (internal/localnet/token/ledger.go resolveLedgerTokenRaw):
// prefer the JWT captured at `up`, fall back to signing a fresh one
// from the cached project's env files. Errors are scrubbed of
// JWT-shaped material before returning so a bearer token can never
// ride out in an error string.
func resolveLedgerJWT(state *registry.State, role string) (string, error) {
	// Path 1: credentials captured by RunUp's captureCredentials.
	if c, ok := state.Credentials[role]; ok && c.JWT != "" {
		return c.JWT, nil
	}

	// Path 2: sign a fresh token from env/<role>-auth-on.env. The V2
	// alpha boot can interrupt creds capture even when `up` succeeds,
	// so the captured map may be empty; re-deriving keeps the default
	// (token-less) Explorer commands working without `--token`.
	inputs, err := splice.LoadCredentialInputs(state.ProjectDir)
	if err != nil {
		return "", redactLedgerJWTs(fmt.Errorf(
			"no captured credentials for role %q and could not load env files (%w); "+
				"run `localnet creds --name %s --role %s --format raw` to confirm token "+
				"issuance, or pass --token explicitly",
			role, err, state.Name, role))
	}
	for _, in := range inputs {
		if string(in.Role) == role {
			tok, signErr := splice.SignToken(in)
			if signErr != nil {
				return "", redactLedgerJWTs(fmt.Errorf("sign token for role %q: %w", role, signErr))
			}
			return tok, nil
		}
	}
	return "", redactLedgerJWTs(fmt.Errorf(
		"no captured credentials and no env entry for role %q on instance %q; "+
			"run `localnet creds --name %s --role %s --format raw` to confirm what's "+
			"available, or pass --token explicitly",
		role, state.Name, state.Name, role))
}

// ledgerJWTPattern matches a compact JWS (three base64url segments,
// header conventionally starting `eyJ`). Mirrors the token package's
// jwtPattern so the resolver's error path can't leak a bearer token.
var ledgerJWTPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// redactLedgerJWTs replaces any JWT-shaped substring in err with a
// fixed marker. Defence-in-depth: SignToken / LoadCredentialInputs
// return the token only on success, but a future change could embed
// credential material in an error string headed for the user's
// terminal.
func redactLedgerJWTs(err error) error {
	if err == nil {
		return nil
	}
	masked := ledgerJWTPattern.ReplaceAllString(err.Error(), "[redacted-jwt]")
	if masked == err.Error() {
		return err
	}
	return errors.New(masked)
}
