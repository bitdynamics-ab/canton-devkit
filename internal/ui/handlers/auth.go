package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// SchemaVersion is the handlers-package mirror of
// internal/api/types.SchemaVersion; a parity test in
// schema_pin_test.go asserts the two stay equal. Every top-level
// response in this package carries this version.
const SchemaVersion = 1

// MountAuth installs the auth/credential routes backing the
// dashboard's "Developer setup" card:
//
//	POST /api/instances/{name}/jwt        — issue a JWT for a role
//	GET  /api/instances/{name}/app-config — export endpoints + JWT
//	                                         in one of several
//	                                         well-known formats
//
// The JWT and app-config are derived from the per-instance state.json
// (party IDs, endpoint ports) and signed with Splice LocalNet's
// shared dev secret via internal/splice.SignToken. The same secret
// the CLI's `localnet env` and the embedded Canton participant use,
// so a JWT issued here Just Works against the running ledger.
//
// # Security posture
//
// JWTs returned here are dev-only (HS256 with a hardcoded shared
// secret — see internal/splice/jwt.go). The UI is loopback-only at
// the server layer, so the JWT never crosses the network.
// The "warning: shared dev secret" string already lives in the
// splice package; the frontend renders it on the Developer Setup
// card.
func MountAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/instances/{name}/jwt", handleIssueJWT)
	mux.HandleFunc("GET /api/instances/{name}/app-config", handleAppConfig)
}

// jwtRequest is the POST body the dashboard's Developer setup card
// sends. The handler validates the role against splice.AllRoles()
// and falls back to RoleAppProvider (the dashboard's default) if
// absent. ttl_seconds is read from the request but currently NOT
// propagated into SignToken (which doesn't yet take an expiry);
// marked as TODO so a future signer change picks it up without a
// request-shape break.
type jwtRequest struct {
	Role       string `json:"role,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Audience   string `json:"audience,omitempty"`
}

// jwtResponse is what the dashboard renders into the monospace token
// preview. SchemaVersion is carried so a frontend bundled for v1
// refuses a v2 backend cleanly.
//
// Token always contains the raw LocalNet JWT. LocalNet is loopback-only
// and the token is signed with the documented dev-only secret.
type jwtResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Token         string `json:"token"`
	Party         string `json:"party"`
	Audience      string `json:"audience"`
	Role          string `json:"role"`
	WarningDev    string `json:"warning_dev_secret"`
	ExpiresInSec  int    `json:"expires_in_seconds,omitempty"`
}

// maxAuthBodyBytes caps request bodies for the auth POST. The JSON
// payload is at most a few hundred bytes (role + ttl + audience);
// 4 KiB is generous and catches the regression class where a bug
// (or malicious client) sends an unbounded body and exhausts memory.
const maxAuthBodyBytes = 4 * 1024

// handleIssueJWT signs a fresh JWT for the given instance + role.
//
// 404 if the instance isn't registered, 400 if the role is unknown,
// 500 if the signer fails (shouldn't — HMAC has no failure mode).
func handleIssueJWT(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	s, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeError(w, http.StatusNotFound, "instance not registered", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}

	var req jwtRequest
	// http.MaxBytesReader errors on Read once the cap is exceeded,
	// which json.Decoder surfaces as a decode error we treat as 400.
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	role := splice.Role(req.Role)
	if role == "" {
		role = splice.RoleAppProvider
	}
	if !roleAllowed(role) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("unknown role %q", req.Role), nil)
		return
	}

	audience := req.Audience
	if audience == "" {
		audience = "https://canton.network.global" // splice's default
	}

	// Lookup the user (party ID) from per-role credentials in
	// state.json. Falls back to the role name as the subject if
	// no recorded credential exists — keeps the handler usable
	// before `up` populates credentials.
	user := string(role)
	if cred, ok := s.Credentials[string(role)]; ok && cred.User != "" {
		user = cred.User
	}

	token, err := splice.SignToken(splice.CredentialInputs{
		Role:     role,
		User:     user,
		Audience: audience,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign token", err)
		return
	}

	// JWT issuance is a security-relevant event — every issue gets
	// an audit line with the role, party, and audience. The raw token
	// is intentionally excluded from logs.
	auditJWTIssued(name, string(role), user, audience)

	writeJSON(w, http.StatusOK, jwtResponse{
		SchemaVersion: SchemaVersion,
		Token:         token,
		Party:         user,
		Audience:      audience,
		Role:          string(role),
		WarningDev:    splice.DevSecretWarning,
		ExpiresInSec:  req.TTLSeconds, // TODO: plumb into SignToken
	})
}

// auditJWTIssued writes a single audit line per JWT issuance.
// Format is stable (parseable by log scrapers) and intentionally
// does NOT include the raw token — even an audit log shouldn't
// carry the secret.
func auditJWTIssued(instance, role, party, audience string) {
	log.Printf("audit: jwt_issued instance=%q role=%q party=%q audience=%q",
		instance, role, party, audience)
}

// roleAllowed checks against the canonical role set. Adding a new
// role anywhere (splice.AllRoles, here, the dashboard dropdown)
// is the right friction — the three places stay in sync via the
// human review that lands the change.
func roleAllowed(r splice.Role) bool {
	for _, ok := range splice.AllRoles() {
		if r == ok {
			return true
		}
	}
	return false
}

// appConfigFormat is the query-param `format=` whitelist.
type appConfigFormat string

const (
	formatEnv  appConfigFormat = "env"  // .env style (default)
	formatJSON appConfigFormat = "json" // JSON object
	formatYAML appConfigFormat = "yaml" // YAML mapping
)

// handleAppConfig: GET /api/instances/{name}/app-config?format=env
//
// Returns the credentials + endpoints needed to point an external
// dApp at this LocalNet instance, rendered on the dashboard's
// Developer setup card.
//
// The payload is the SHARED apitypes.EnvExport built by
// localnet.BuildEnvExport — the exact same shape and value set the
// CLI's `dpm localnet env` emits, so the two surfaces can't drift:
// participant Ledger / Admin / JSON API ports, the scan UI URL, and
// the real on-ledger party ids. See CONTRIBUTING.md "CLI ↔ Web UI
// parity".
//
// JWTs are always included as raw LocalNet credentials. Defaults to
// format=env.
func handleAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}

	format := appConfigFormat(r.URL.Query().Get("format"))
	if format == "" {
		format = formatEnv
	}
	// Validate the format BEFORE building the export so a bad
	// ?format= is a clean 400 regardless of instance state.
	if format != formatEnv && format != formatJSON && format != formatYAML {
		writeError(w, http.StatusBadRequest,
			"format must be one of env|json|yaml", nil)
		return
	}

	ex, err := localnet.BuildEnvExport(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeError(w, http.StatusNotFound, "instance not registered", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}

	switch format {
	case formatEnv:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(renderEnv(ex)))
	case formatJSON:
		writeJSON(w, http.StatusOK, ex)
	case formatYAML:
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(renderYAML(ex)))
	}
}

// sortedVarKeys returns the EnvExport var keys in stable lexical
// order. Map iteration order is randomised in Go, so without this the
// env/yaml bodies would differ byte-for-byte between requests — bad
// for diffing, caching, and golden-test comparisons (the CLI's shell
// renderer sorts for the same reason).
func sortedVarKeys(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderEnv emits the .env shape the dashboard's "env" tab shows.
// One KEY=value line per EnvExport var, in stable key order. The keys
// are already in CANTON_<...> form (the CLI's canonical env names), so
// no extra capitalisation is needed.
func renderEnv(ex apitypes.EnvExport) string {
	var b []byte
	b = append(b, fmt.Sprintf("# %s · splice %s\n", ex.Instance, ex.Vars["CANTON_SPLICE_VERSION"])...)
	for _, k := range sortedVarKeys(ex.Vars) {
		b = append(b, fmt.Sprintf("%s=%s\n", k, ex.Vars[k])...)
	}
	return string(b)
}

// renderYAML emits a minimal YAML rendering — we don't pull a YAML
// dep for one handler; the structure is shallow enough that
// hand-rolling is cleaner than adding gopkg.in/yaml.v3. Keys are
// emitted in stable order under a single `vars:` mapping, mirroring
// the EnvExport JSON shape.
func renderYAML(ex apitypes.EnvExport) string {
	var b []byte
	b = append(b, fmt.Sprintf("instance: %s\n", ex.Instance)...)
	b = append(b, fmt.Sprintf("schema_version: %d\n", ex.SchemaVersion)...)
	b = append(b, "vars:\n"...)
	for _, k := range sortedVarKeys(ex.Vars) {
		b = append(b, fmt.Sprintf("  %s: %s\n", k, ex.Vars[k])...)
	}
	return string(b)
}
