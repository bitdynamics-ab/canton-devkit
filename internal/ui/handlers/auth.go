package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// MountAuth installs the auth/credential routes — the surfaces the
// 2026-05-25 dashboard refresh added as a "Developer setup" card:
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
// the server layer (BIT-129), so the JWT never crosses the network.
// The "warning: shared dev secret" string already lives in the
// splice package; the frontend renders it on the Developer Setup
// card.
func MountAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/instances/{name}/jwt", handleIssueJWT)
	mux.HandleFunc("GET /api/instances/{name}/app-config", handleAppConfig)
}

// jwtRequest is the POST body the dashboard sends. Mirrors the
// chip-button shape in webui-dashboard.jsx's Developer setup card:
// the user picks a party/role from a dropdown, a ttl from a chip
// row, and an audience.
//
// The handler validates the role against splice.AllRoles() and
// falls back to RoleAppProvider if absent — that's the dashboard's
// default selection. ttl_seconds is read from the request but
// currently NOT propagated into SignToken (which doesn't yet take
// an expiry); marked as TODO so a future signer change picks it up
// without a request-shape break.
type jwtRequest struct {
	Role       string `json:"role,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Audience   string `json:"audience,omitempty"`
}

// jwtResponse is what the dashboard renders into the monospace token
// preview. The header/payload/signature split mirrors the colored
// triplet in the JSX mockup — the frontend re-splits on `.` for
// rendering but we send the whole token plus the components for
// convenience.
type jwtResponse struct {
	Token        string `json:"token"`
	Party        string `json:"party"`
	Audience     string `json:"audience"`
	Role         string `json:"role"`
	WarningDev   string `json:"warning_dev_secret"`
	ExpiresInSec int    `json:"expires_in_seconds,omitempty"`
}

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
	// Empty body is OK — the dashboard default-clicks before
	// surfacing the dropdowns. json.Decoder treats EOF as no-op.
	_ = json.NewDecoder(r.Body).Decode(&req)
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
	writeJSON(w, http.StatusOK, jwtResponse{
		Token:        token,
		Party:        user,
		Audience:     audience,
		Role:         string(role),
		WarningDev:   splice.DevSecretWarning,
		ExpiresInSec: req.TTLSeconds, // TODO(BIT-141): plumb into SignToken
	})
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
// dApp at this LocalNet instance. The 2026-05-25 dashboard refresh
// renders this on the Developer setup card with format-switching
// chip buttons.
//
// Defaults to format=env (the most-clicked tab in the mockup).
func handleAppConfig(w http.ResponseWriter, r *http.Request) {
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

	format := appConfigFormat(r.URL.Query().Get("format"))
	if format == "" {
		format = formatEnv
	}

	// Build the shared payload first, then render per-format.
	// Keeps the per-format rendering branches small.
	cfg := appConfigPayload{
		Name:          s.Name,
		SpliceVersion: s.SpliceVersion,
		Endpoints:     endpointsFromPorts(s.Ports),
		Parties:       partiesFromCredentials(s.Credentials),
	}

	switch format {
	case formatEnv:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(renderEnv(cfg)))
	case formatJSON:
		writeJSON(w, http.StatusOK, cfg)
	case formatYAML:
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(renderYAML(cfg)))
	default:
		writeError(w, http.StatusBadRequest,
			"format must be one of env|json|yaml", nil)
	}
}

// appConfigPayload is the canonical shape across all output
// formats. Renderers project it into env / json / yaml.
type appConfigPayload struct {
	Name          string            `json:"name" yaml:"name"`
	SpliceVersion string            `json:"splice_version" yaml:"splice_version"`
	Endpoints     map[string]string `json:"endpoints" yaml:"endpoints"`
	Parties       map[string]string `json:"parties" yaml:"parties"`
}

// endpointsFromPorts converts the registry's port map into a
// localhost-URL view useful for an external app. Only the
// user-facing ports (UIs, postgres) are surfaced; internal admin
// gRPC ports stay hidden.
func endpointsFromPorts(ports map[string]int) map[string]string {
	out := map[string]string{}
	mapping := map[string]string{
		"app_user_ui":     "app_user_ui",
		"app_provider_ui": "app_provider_ui",
		"sv_ui":           "sv_ui",
		"swagger_ui":      "swagger_ui",
		"postgres":        "postgres",
	}
	for src, dst := range mapping {
		if p, ok := ports[src]; ok && p > 0 {
			out[dst] = fmt.Sprintf("http://localhost:%d", p)
		}
	}
	return out
}

// partiesFromCredentials projects the per-role credential map into a
// simple role→partyID view. Strips the JWT (the JWT endpoint above is
// the way to obtain one; this endpoint is for identity discovery).
func partiesFromCredentials(creds map[string]registry.Credential) map[string]string {
	out := map[string]string{}
	for role, c := range creds {
		if c.User != "" {
			out[role] = c.User
		}
	}
	return out
}

// renderEnv emits the .env shape the dashboard's "env" tab shows.
// One KEY=value line per logical entry, capitalised to standard
// dotenv conventions. Endpoints become LEDGER_HOST/_PORT pairs
// (which is what most Daml/Canton tooling expects).
func renderEnv(c appConfigPayload) string {
	var b []byte
	b = append(b, fmt.Sprintf("# %s · splice %s\n", c.Name, c.SpliceVersion)...)
	for k, v := range c.Endpoints {
		b = append(b, fmt.Sprintf("%s=%s\n", upperEnv(k), v)...)
	}
	for role, party := range c.Parties {
		b = append(b, fmt.Sprintf("PARTY_%s=%s\n", upperEnv(role), party)...)
	}
	return string(b)
}

// renderYAML emits a minimal YAML rendering — we don't pull a YAML
// dep for one handler; the structure is shallow enough that
// hand-rolling is cleaner than adding gopkg.in/yaml.v3.
func renderYAML(c appConfigPayload) string {
	var b []byte
	b = append(b, fmt.Sprintf("name: %s\n", c.Name)...)
	b = append(b, fmt.Sprintf("splice_version: %s\n", c.SpliceVersion)...)
	b = append(b, "endpoints:\n"...)
	for k, v := range c.Endpoints {
		b = append(b, fmt.Sprintf("  %s: %s\n", k, v)...)
	}
	b = append(b, "parties:\n"...)
	for k, v := range c.Parties {
		b = append(b, fmt.Sprintf("  %s: %s\n", k, v)...)
	}
	return string(b)
}

// upperEnv uppercases and replaces dashes/dots with underscores.
// Pulled out so the env renderer reads cleanly. ASCII-only by
// design — the registry rejects non-ASCII names.
func upperEnv(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c-32)
		case c == '-' || c == '.':
			out = append(out, '_')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
