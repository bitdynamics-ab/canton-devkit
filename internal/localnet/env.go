package localnet

import (
	"encoding/json"
	"fmt"
	"os"
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
	// CANTON_AUTH_FILE points at the per-instance auth.json a user can
	// load with `jq` (<DataDir>/auth.json). The path was emitted even
	// though NOTHING ever wrote the file, so a script doing
	// `jq < $CANTON_AUTH_FILE` hit ENOENT. writeAuthFile now
	// materialises it (best-effort, 0600 — it carries the dev JWTs)
	// whenever the data dir + credentials are present, so the path
	// resolves for a healthy instance. The var is still always emitted
	// (it documents the conventional location); the write is the fix.
	authFile := filepath.Join(state.DataDir, "auth.json")
	writeAuthFile(authFile, state.Credentials)
	out.Vars["CANTON_AUTH_FILE"] = authFile

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

// authFileEntry is one role's credentials as written to auth.json. It
// mirrors registry.Credential but is declared here so the on-disk
// auth-file shape is owned by the env layer that emits CANTON_AUTH_FILE.
type authFileEntry struct {
	Role     string `json:"role"`
	User     string `json:"user"`
	Audience string `json:"audience"`
	JWT      string `json:"jwt"`
}

// writeAuthFile materialises auth.json (a 0600 JSON object keyed by
// role, so a script can `jq -r '."app-provider".jwt' "$CANTON_AUTH_FILE"`)
// next to the instance's data dir. This is what makes CANTON_AUTH_FILE
// resolve — previously nothing wrote it.
//
// Best-effort: it is a no-op when there are no credentials yet, the
// parent data dir doesn't exist (a stale/half-removed instance — we
// never MkdirAll one), or marshalling/writing fails. Callers always
// emit the path regardless; a missing parent dir is an abnormal-instance
// edge, not the common case this fixes. Safe to call on every env export
// — both the CLI `env` command and the Web UI app-config handler do.
//
// The write goes through a temp-file-in-same-dir + explicit Chmod(0600)
// + Rename rather than os.WriteFile, mirroring registry.atomicWrite. This
// is rerun on every export (not skipped when the file already exists) so
// a pre-existing world-readable auth.json is re-tightened to 0600 — the
// file carries dev JWTs — and the Chmod is umask-independent. The rename
// is atomic, so a crash mid-write can never leave partial JSON behind.
func writeAuthFile(path string, creds map[string]registry.Credential) {
	if len(creds) == 0 {
		return
	}
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return // data dir absent — don't create a tree for a stale instance
	}
	entries := make(map[string]authFileEntry, len(creds))
	for role, c := range creds {
		entries[role] = authFileEntry{Role: c.Role, User: c.User, Audience: c.Audience, JWT: c.JWT}
	}
	body, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".tmp-auth-*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return
	}
	// Chmod explicitly (umask-independent) so the temp — and the file it
	// becomes after the rename — is 0600 regardless of the process umask.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return
	}
	committed = true
}
