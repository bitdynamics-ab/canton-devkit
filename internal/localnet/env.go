package localnet

import (
	"fmt"
	"path/filepath"
	"strings"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// Shared env export. Both the CLI (`dpm localnet env`) and the Web UI
// (`GET /api/instances/{name}/app-config`) MUST surface the same
// endpoint + credential + party set so an operator who learns the
// "point a dApp at this LocalNet" workflow on one surface finds the
// identical values on the other. The two surfaces used to be
// independent re-implementations with divergent shapes — the UI hid
// the participant Ledger/Admin/JSON API ports a dApp actually needs —
// so the builder below is the single source of truth they both call.
// See AGENTS.md "CLI ↔ Web UI parity".

// EnvJWTRedaction is the placeholder that replaces a captured JWT when
// the caller does not opt into raw tokens. Format chosen so a
// downstream shell that expects a real token errors loudly ("Bearer
// <redacted>" -> 401) instead of silently sending an empty header.
// Mirrored by the CLI's jwtRedaction and the UI's
// jwtRedactionPlaceholder — same sentinel so the surfaces don't drift.
const EnvJWTRedaction = "<redacted>"

// scanUIVHost is the nginx virtual-host name the Splice scan UI /
// scan registry routes live under on LocalNet. The scan app
// (`splice:5012` inside the docker network) is exposed on the host
// behind the SV UI port under `server_name scan.localhost` (see
// cluster/compose/localnet/conf/nginx/sv.conf, mirrored by
// internal/localnet/token.scanVHost). The proposal lists "scan UI" as
// a distinct env value, so we emit an explicit CANTON_SCAN_UI_URL that
// carries this host hint rather than leaving operators to derive it
// from the bare sv_ui port.
const scanUIVHost = "scan.localhost"

// BuildEnvExport assembles the shared apitypes.EnvExport for an
// instance from its registry state. It is the single builder behind
// both the CLI `env` command and the Web UI app-config handler.
//
// Sources, in the order they populate Vars:
//
//  1. A small set of stable convenience keys (CANTON_INSTANCE,
//     CANTON_SPLICE_VERSION, CANTON_AUTH_FILE) so scripts can branch
//     without re-reading state.json.
//  2. state.Ports -> CANTON_<UPPER>_PORT for every logical name. This
//     deliberately includes the participant_ledger/admin/json_<role>
//     ports captured by CaptureCantonPorts AND the sv_ui (scan UI)
//     port — exactly the endpoints an external dApp needs and which
//     the old UI export hid.
//  3. A derived CANTON_SCAN_UI_URL when the sv_ui port is recorded,
//     carrying the scan.localhost vhost hint (the proposal names the
//     scan UI as a distinct value).
//  4. state.Credentials -> CANTON_<ROLE>_JWT (redacted unless
//     includeJWT) plus the user/audience pair that signed it.
//  5. state.Parties -> CANTON_<ROLE>_PARTY for the role parties
//     (app-user/app-provider/sv), carrying the REAL on-ledger party
//     ids. These are distinct from the ledger-api user name in
//     Credentials — conflating the two (as the old UI did) is the
//     defect this fixes. Empty until `up`/token tooling has scanned
//     the participants, so a key is only emitted when a party id is
//     actually recorded.
func BuildEnvExport(name string, includeJWT bool) (apitypes.EnvExport, error) {
	state, err := registry.Read(name)
	if err != nil {
		return apitypes.EnvExport{}, err
	}
	out := apitypes.EnvExport{
		SchemaVersion: apitypes.SchemaVersion,
		Instance:      name,
		Vars: make(map[string]string,
			len(state.Ports)*2+len(state.Credentials)*3+len(state.Parties)+4),
	}

	out.Vars["CANTON_INSTANCE"] = name
	out.Vars["CANTON_SPLICE_VERSION"] = state.SpliceVersion
	// AuthFile points at the per-instance auth.json the user can load
	// with `jq` (~/.canton-devkit/<name>/auth.json). filepath.Join (not
	// "/" concat) so the path is correct on Windows and doesn't
	// duplicate separators if state.DataDir has a trailing slash.
	out.Vars["CANTON_AUTH_FILE"] = filepath.Join(state.DataDir, "auth.json")

	for logical, port := range state.Ports {
		out.Vars[PortEnvKey(logical)] = fmt.Sprintf("%d", port)
	}
	// The scan UI is reachable on the SV UI port behind the
	// scan.localhost nginx vhost. Surface it under an explicit,
	// self-describing key so a dApp doesn't have to know the vhost
	// trick. Skipped when sv_ui wasn't captured (instance pre-dates
	// port capture, or came up without the SV profile).
	if port, ok := state.Ports["sv_ui"]; ok && port > 0 {
		out.Vars["CANTON_SCAN_UI_URL"] = fmt.Sprintf("http://%s:%d", scanUIVHost, port)
	}

	for role, cred := range state.Credentials {
		base := CredEnvKeyPrefix(role)
		if includeJWT {
			out.Vars[base+"_JWT"] = cred.JWT
		} else {
			// Default: emit a non-empty redaction placeholder so
			// downstream tooling that asserts the variable is set keeps
			// working, but the dev-only signing secret never hits
			// stdout / CI logs / shared terminals.
			out.Vars[base+"_JWT"] = EnvJWTRedaction
		}
		if cred.User != "" {
			out.Vars[base+"_USER"] = cred.User
		}
		if cred.Audience != "" {
			out.Vars[base+"_AUDIENCE"] = cred.Audience
		}
	}

	// Real party ids, keyed by alias -> CANTON_<ALIAS>_PARTY. The role
	// parties are seeded under an alias equal to the role name
	// ("app-user", "app-provider", "sv") so a dApp can submit on behalf
	// of a role's party (CANTON_APP_USER_PARTY) without a separate scan
	// call; developer-created aliases (`party new bob`) get the same
	// treatment (CANTON_BOB_PARTY). Aliases validate against
	// ^[a-z][a-z0-9-]{0,62}$ (see token.validAlias), so the normalised
	// key is always a valid shell identifier. Skipped when the PartyID
	// hasn't been resolved yet (a key with an empty value is worse than
	// an absent one — see CaptureCantonPorts' "missing means unknown").
	for alias, party := range state.Parties {
		if party.PartyID == "" {
			continue
		}
		out.Vars[CredEnvKeyPrefix(alias)+"_PARTY"] = party.PartyID
	}

	return out, nil
}

// PortEnvKey converts a logical port name ("app_user_ui" or
// "app-provider-ui") into the canonical CANTON_<UPPER>_PORT env-var
// key. Both underscore and hyphen are normalised because the state
// file's Port keys vary across adapter versions.
func PortEnvKey(logical string) string {
	return "CANTON_" + normalizeEnvSegment(logical) + "_PORT"
}

// CredEnvKeyPrefix turns a role/alias label ("sv", "app-user") into the
// shared prefix for that role's CANTON_ env vars. Mirrors PortEnvKey's
// normalisation rules so a downstream consumer sees a consistent
// CANTON_APP_USER_* set whether the role is hyphenated or underscored
// upstream.
func CredEnvKeyPrefix(role string) string {
	return "CANTON_" + normalizeEnvSegment(role)
}

// normalizeEnvSegment upper-cases a logical name and replaces hyphens
// with underscores so a value like "app-user-ui" produces
// APP_USER_UI. Shared by the port and credential key builders so the
// two never drift.
func normalizeEnvSegment(s string) string {
	return strings.ReplaceAll(strings.ToUpper(s), "-", "_")
}
