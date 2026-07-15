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
// endpoint + credential + party set, so the builder below is the single
// source of truth they both call. See CONTRIBUTING.md
// "CLI ↔ Web UI parity".

// EnvJWTRedaction is the placeholder that replaces a captured JWT when
// the caller does not opt into raw tokens. Format chosen so a
// downstream shell that expects a real token errors loudly ("Bearer
// <redacted>" -> 401) instead of silently sending an empty header.
// Mirrored by the CLI's jwtRedaction and the UI's
// jwtRedactionPlaceholder — same sentinel so the surfaces don't drift.
const EnvJWTRedaction = "<redacted>"

// Splice UIs/APIs are served on LocalNet behind instance-scoped nginx
// virtual hosts of the form <service>.<instance>.localhost (see
// WriteNginxVhostOverlay and instanceVHost). We surface those hosts in
// explicit, self-describing URL vars (CANTON_SCAN_UI_URL,
// CANTON_*_LEDGER_API_URL) rather than leaving operators to derive them
// from the bare UI ports.
//
// *.localhost resolves to 127.0.0.1 in browsers, curl, and Go, but NOT
// in the JVM/Node/Python resolvers (and some Rust HTTP clients) — those
// need an explicit Host header (HTTP) / :authority pseudo-header (gRPC)
// or an /etc/hosts entry.

// BuildEnvExport assembles the shared apitypes.EnvExport for an
// instance from its registry state. It is the single builder behind
// both the CLI `env` command and the Web UI app-config handler.
//
// Sources, in the order they populate Vars:
//
//  1. A small set of stable convenience keys (CANTON_INSTANCE,
//     CANTON_SPLICE_VERSION, CANTON_AUTH_FILE) so scripts can branch
//     without re-reading state.json.
//  2. state.Ports -> CANTON_<UPPER>_PORT for every logical name,
//     including the participant_ledger/admin/json_<role> ports captured
//     by CaptureCantonPorts and the sv_ui (scan UI) port — exactly the
//     endpoints an external dApp needs.
//  3. A derived CANTON_SCAN_UI_URL when the sv_ui port is recorded, plus
//     per-role CANTON_<ROLE>_{JSON,GRPC}_LEDGER_API_URL and unqualified
//     CANTON_{JSON,GRPC}_LEDGER_API_URL aliases, each carrying the
//     matching instance-scoped <service>.<instance>.localhost vhost.
//  4. state.Credentials -> CANTON_<ROLE>_JWT (redacted unless
//     includeJWT) plus the user/audience pair that signed it.
//  5. state.Parties -> CANTON_<ROLE>_PARTY for the role parties
//     (app-user/app-provider/sv), carrying the REAL on-ledger party
//     ids — distinct from the ledger-api user name in Credentials.
//     Empty until `up`/token tooling has scanned the participants, so
//     a key is only emitted when a party id is actually recorded.
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
	// load with `jq` (<DataDir>/auth.json). writeAuthFile materialises
	// it (best-effort, 0600 — it carries the dev JWTs) when the data dir
	// and credentials are present, so the path resolves for a healthy
	// instance. The var is always emitted (it documents the conventional
	// location) regardless of whether the write succeeded.
	authFile := filepath.Join(state.DataDir, "auth.json")
	writeAuthFile(authFile, state.Credentials)
	out.Vars["CANTON_AUTH_FILE"] = authFile

	for logical, port := range state.Ports {
		out.Vars[PortEnvKey(logical)] = fmt.Sprintf("%d", port)
	}
	// The scan UI is reachable on the SV UI port behind the
	// scan.<instance>.localhost nginx vhost. Surface it under an
	// explicit, self-describing key so a dApp doesn't have to know the
	// vhost trick. Skipped when sv_ui wasn't captured (instance
	// pre-dates port capture, or came up without the SV profile).
	if port, ok := state.Ports["sv_ui"]; ok && port > 0 {
		out.Vars["CANTON_SCAN_UI_URL"] = fmt.Sprintf("http://%s:%d",
			instanceVHost(VHostServiceScan, name), port)
	}

	// The JSON and gRPC Ledger APIs are reachable on each role's UI port
	// behind the json-ledger-api / grpc-ledger-api instance-scoped
	// vhosts. Emit a per-role URL var for every recorded role UI port,
	// plus unqualified CANTON_{JSON,GRPC}_LEDGER_API_URL aliases pointing
	// at the app-provider participant (the common dApp target). gRPC URLs
	// carry no scheme — the host:port is what a gRPC client dials, with
	// the vhost as the :authority pseudo-header.
	ledgerRolePortKeys := map[string]string{
		"app-user":     "app_user_ui",
		"app-provider": "app_provider_ui",
		"sv":           "sv_ui",
	}
	for role, portKey := range ledgerRolePortKeys {
		port, ok := state.Ports[portKey]
		if !ok || port <= 0 {
			continue
		}
		prefix := CredEnvKeyPrefix(role)
		out.Vars[prefix+"_JSON_LEDGER_API_URL"] = fmt.Sprintf("http://%s:%d",
			instanceVHost(VHostServiceJSONLedger, name), port)
		out.Vars[prefix+"_GRPC_LEDGER_API_URL"] = fmt.Sprintf("%s:%d",
			instanceVHost(VHostServiceGRPCLedger, name), port)
	}
	// Unqualified aliases default to the app-provider participant.
	if port, ok := state.Ports["app_provider_ui"]; ok && port > 0 {
		out.Vars["CANTON_JSON_LEDGER_API_URL"] = fmt.Sprintf("http://%s:%d",
			instanceVHost(VHostServiceJSONLedger, name), port)
		out.Vars["CANTON_GRPC_LEDGER_API_URL"] = fmt.Sprintf("%s:%d",
			instanceVHost(VHostServiceGRPCLedger, name), port)
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
// next to the instance's data dir, making CANTON_AUTH_FILE resolve.
//
// Best-effort: a no-op when there are no credentials, the parent data
// dir doesn't exist (a stale instance — we never MkdirAll one), or
// marshalling/writing fails. Safe to call on every env export.
//
// The write goes through a temp-file-in-same-dir + explicit Chmod(0600)
// + Rename (mirroring registry.atomicWrite): the rewrite runs on every
// export so a pre-existing world-readable auth.json is re-tightened to
// 0600 (umask-independent), and the atomic rename never leaves partial
// JSON behind on a crash.
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
