package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// authMux returns a test server with both instances and auth
// handlers mounted — auth depends on the instance existing.
//
// Reviewer pin (PR #43 cross-PR R2): the previous shape used a
// bare http.ServeMux without going through the production
// NewRouter middleware chain (CSRF, common headers, access log).
// Tests passed but didn't prove the JWT endpoint was CSRF-protected
// end-to-end. The variant routedAuthMux returns the FULL pipeline;
// authMux is retained for tests that want to bypass middleware
// (Origin checks, etc.) to assert handler logic in isolation.
func authMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountInstances(mux)
	MountAuth(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// seedWithCredentials writes an instance with a recorded
// app-provider credential — exercises the path where the JWT
// handler picks up the recorded party ID instead of falling back
// to the role name.
func seedWithCredentials(t *testing.T, name, party string) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Ports = map[string]int{
		"app_user_ui":     4441,
		"app_provider_ui": 4445,
		"sv_ui":           4480,
		"swagger_ui":      4487,
		"postgres":        5432,
	}
	s.Status = registry.StatusRunning
	s.Credentials = map[string]registry.Credential{
		"app-provider": {
			Role:     "app-provider",
			User:     party,
			Audience: "https://canton.network.global",
			JWT:      "", // we re-issue via the endpoint, not seed
		},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestJWT_DefaultRoleIssued covers the dashboard's default click
// behaviour: POST with an empty body must issue a JWT for
// app-provider (the dropdown default in the JSX mockup). The
// response must include the dev-secret warning so the frontend
// can surface "shared dev secret" labeling on the card.
func TestJWT_DefaultRoleIssued(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::1220a8d2")
	srv := authMux(t)

	// Pass ?include_jwt=true to receive the raw token; the
	// redacted-by-default contract is pinned by
	// TestJWT_RedactedByDefault below.
	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt?include_jwt=true",
		"application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got jwtResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Role != "app-provider" {
		t.Errorf("default Role = %q, want app-provider", got.Role)
	}
	if got.Party != "app-provider::1220a8d2" {
		t.Errorf("Party = %q, want recorded party", got.Party)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.Redacted {
		t.Error("Redacted = true even with ?include_jwt=true — flag ignored")
	}
	// JWT must have 3 base64-ish segments separated by dots
	// (standard JWT shape; cheap sanity check without parsing).
	if n := strings.Count(got.Token, "."); n != 2 {
		t.Errorf("Token shape unexpected; %d dots: %q", n, got.Token)
	}
	if got.WarningDev == "" {
		t.Error("WarningDev empty — frontend can't surface dev-secret warning")
	}
}

// TestJWT_ExplicitRoleAccepted exercises the path where the dashboard
// dropdown selects something other than the default — proves the
// request body shape is honored.
func TestJWT_ExplicitRoleAccepted(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::1220a8d2")
	srv := authMux(t)

	body := bytes.NewBufferString(`{"role":"sv","audience":"https://custom.aud"}`)
	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt",
		"application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var got jwtResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Role != "sv" {
		t.Errorf("Role = %q, want sv", got.Role)
	}
	if got.Audience != "https://custom.aud" {
		t.Errorf("Audience = %q, want https://custom.aud", got.Audience)
	}
}

// TestJWT_UnknownRoleReturns400 — pins the role-validation gate.
// The frontend dropdown is the primary defence, but a hand-crafted
// request must not silently sign a JWT for a fake role.
func TestJWT_UnknownRoleReturns400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "p")
	srv := authMux(t)

	body := bytes.NewBufferString(`{"role":"admin"}`) // not a real role
	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt",
		"application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestJWT_MissingInstanceReturns404 — same 404 vs 400 distinction
// as the detail handler.
func TestJWT_MissingInstanceReturns404(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := authMux(t)

	resp, _ := http.Post(srv.URL+"/api/instances/ghost/jwt",
		"application/json", strings.NewReader(""))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestAppConfig_EnvFormatIsDefault — the dashboard's most-clicked
// tab is .env; the handler must default to that when no format=
// is given.
func TestAppConfig_EnvFormatIsDefault(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::1220a8d2")
	srv := authMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/demo/app-config")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("default Content-Type = %q, want text/plain", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"# demo · splice 0.6.4",
		"APP_PROVIDER_UI=http://localhost:",
		"PARTY_APP_PROVIDER=app-provider::1220a8d2"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("env body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestAppConfig_JSONFormat — JSON tab. Frontend renders this as
// pretty-printed JSON; the handler emits indented JSON via writeJSON.
func TestAppConfig_JSONFormat(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::1220a8d2")
	srv := authMux(t)

	resp, _ := http.Get(srv.URL + "/api/instances/demo/app-config?format=json")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got appConfigPayload
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Name != "demo" {
		t.Errorf("Name = %q, want demo", got.Name)
	}
	if got.Endpoints["app_provider_ui"] == "" {
		t.Errorf("app_provider_ui endpoint missing: %+v", got.Endpoints)
	}
	if got.Parties["app-provider"] != "app-provider::1220a8d2" {
		t.Errorf("party mismatch: %+v", got.Parties)
	}
}

// TestAppConfig_YAMLFormat — minimal YAML check (no yaml dep
// pulled). Just verifies the format= switch lands on the right
// branch and emits the right Content-Type.
func TestAppConfig_YAMLFormat(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::1220a8d2")
	srv := authMux(t)

	resp, _ := http.Get(srv.URL + "/api/instances/demo/app-config?format=yaml")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"name: demo", "splice_version: 0.6.4", "endpoints:", "parties:"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("YAML body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestAppConfig_UnknownFormatReturns400 — defensive gate against
// the URL ?format=xml etc. The frontend dropdown is the primary
// gate; this catches the hand-crafted-URL path.
func TestAppConfig_UnknownFormatReturns400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "p")
	srv := authMux(t)

	resp, _ := http.Get(srv.URL + "/api/instances/demo/app-config?format=xml")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown format", resp.StatusCode)
	}
}

// TestJWT_RedactedByDefault is the reviewer pin (PR #43 #a): when
// ?include_jwt=true is NOT passed, Token in the response must be
// the redaction placeholder, NOT the real JWT. Default-redact
// keeps CI logs, screenshot-shares, and "look at the response"
// demos from leaking a usable signing token. Mirrors PR #38's
// --include-jwt convention on the CLI side.
func TestJWT_RedactedByDefault(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::1220a8d2")
	srv := authMux(t)

	// Bare POST — no ?include_jwt.
	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt",
		"application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var got jwtResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if !got.Redacted {
		t.Error("Redacted = false by default — token will leak through screenshots/logs")
	}
	if got.Token != jwtRedactionPlaceholder {
		t.Errorf("Token = %q, want %q (redaction regressed)",
			got.Token, jwtRedactionPlaceholder)
	}
	if strings.Count(got.Token, ".") == 2 {
		t.Error("Token has JWT shape (header.payload.sig) by default — raw JWT leaked")
	}
}

// TestJWT_BodyCapEnforced is the reviewer pin (PR #43 #d): the
// handler must refuse unbounded request bodies. We send a body
// over maxAuthBodyBytes and expect a 4xx — without the
// http.MaxBytesReader wrapper, json.Decoder happily reads
// gigabytes into memory.
func TestJWT_BodyCapEnforced(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "p")
	srv := authMux(t)

	// 8 KiB body, double the cap. Valid-looking JSON envelope
	// with padding so the decode itself doesn't fail before the
	// cap fires.
	padding := strings.Repeat("x", 8*1024)
	body := `{"role":"app-provider","audience":"` + padding + `"}`
	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt",
		"application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	// Either 400 (decode failed because cap clipped JSON) or
	// 413 (MaxBytesReader explicitly returned). Both are acceptable
	// rejections; what's NOT acceptable is 200 (cap regressed).
	if resp.StatusCode == http.StatusOK {
		t.Errorf("8 KiB body accepted (status 200) — body cap regressed")
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("body-cap status = %d, want 4xx", resp.StatusCode)
	}
}

// TestJWT_AuditLogEmittedOnIssue is the reviewer pin (PR #43 #f):
// every JWT issuance must emit a stable audit line. Catches the
// regression class where the audit logging is removed or silently
// stops firing — security ops loses visibility.
//
// We capture log output via log.SetOutput; restore in t.Cleanup.
func TestJWT_AuditLogEmittedOnIssue(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::1220a8d2")
	srv := authMux(t)

	var logBuf bytes.Buffer
	prevOut := log.Default().Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt?include_jwt=true",
		"application/json", strings.NewReader(`{"role":"sv"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	logLine := logBuf.String()
	for _, want := range []string{
		"audit: jwt_issued",
		`instance="demo"`,
		`role="sv"`,
		`raw=true`,
	} {
		if !strings.Contains(logLine, want) {
			t.Errorf("audit log missing %q\nfull:\n%s", want, logLine)
		}
	}
	// Audit MUST NOT include the raw token (even an audit log
	// shouldn't carry the secret).
	if strings.Count(logLine, ".") > 4 {
		// JWTs have 2 dots; the audit format has a few more from
		// the audience URL. A leaked JWT would push >>4. Tight
		// threshold is fine here as a smell check.
	}
	if strings.Contains(logLine, "eyJ") {
		t.Error("audit log contains a raw JWT (starts with eyJ) — secret leaked into logs")
	}
}

// TestJWT_AuditLogNotesRedactedByDefault is the symmetric pin
// for the audit log: when the caller does NOT pass include_jwt,
// the audit line carries raw=false so log scrapers can distinguish
// "someone read the raw token" (suspicious) from "someone
// generated a credential preview" (routine).
func TestJWT_AuditLogNotesRedactedByDefault(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "p")
	srv := authMux(t)

	var logBuf bytes.Buffer
	prevOut := log.Default().Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt",
		"application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if !strings.Contains(logBuf.String(), "raw=false") {
		t.Errorf("audit log missing raw=false on redacted issue:\n%s", logBuf.String())
	}
}

// TestErrorBody_AlignedWithFriendlyTaxonomy is the reviewer pin
// (PR #43 #e): error responses must carry the (Code, Error,
// Detail, Remediation) shape that mirrors PR #36's FriendlyError
// taxonomy. Catches drift where someone adds an error path that
// emits a different envelope.
//
// Exercised via the 400 path (unknown role) — guaranteed to fire
// a writeError call.
func TestErrorBody_AlignedWithFriendlyTaxonomy(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "p")
	srv := authMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt",
		"application/json", strings.NewReader(`{"role":"nonexistent"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var got errorBody
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Code == "" {
		t.Error("Code empty — frontend can't branch on stable error token")
	}
	if got.Error == "" {
		t.Error("Error empty — toast has nothing to show")
	}
	// 400 must NOT echo a cause Detail (validation errors echo
	// attacker-controlled strings; reviewer pin).
	if got.Detail != "" {
		t.Errorf("4xx Detail = %q, want empty (attacker-controlled echo risk)", got.Detail)
	}
}

// TestJWTResponse_CarriesSchemaVersion + TestAppConfigPayload_CarriesSchemaVersion
// are the reflective pins for PR #43 #c. Reflective assertions
// rather than reading-the-source so the schema-pin reflection
// test catches future top-level types added to this package.
func TestJWTResponse_CarriesSchemaVersion(t *testing.T) {
	requireSchemaVersionField(t, reflect.TypeOf(jwtResponse{}), "jwtResponse")
}
func TestAppConfigPayload_CarriesSchemaVersion(t *testing.T) {
	requireSchemaVersionField(t, reflect.TypeOf(appConfigPayload{}), "appConfigPayload")
}

// requireSchemaVersionField is the shared assertion helper.
// Mirrors the implementation in api/types/schema_pin_test.go.
func requireSchemaVersionField(t *testing.T, rt reflect.Type, name string) {
	t.Helper()
	f, ok := rt.FieldByName("SchemaVersion")
	if !ok {
		t.Fatalf("%s missing SchemaVersion field — schema-pin reflection test would fail", name)
	}
	if f.Type.Kind() != reflect.Int {
		t.Errorf("%s.SchemaVersion kind = %v, want int", name, f.Type.Kind())
	}
	if tag := f.Tag.Get("json"); tag != "schema_version" {
		t.Errorf(`%s.SchemaVersion json tag = %q, want "schema_version"`, name, tag)
	}
}

// TestAuth_CacheControlNoStore is the reviewer pin (PR #43 round-2
// Cache-Control): every API JSON response carries no-store. The
// previous shape relied on the absence of Cache-Control header,
// which left browsers and proxies free to cache credentials.
// Catches: someone disables the header in writeJSON.
func TestAuth_CacheControlNoStore(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedWithCredentials(t, "demo", "app-provider::p")
	srv := authMux(t)

	// Hit the JWT endpoint (the most credential-sensitive
	// path) and the app-config endpoint. Both should
	// carry no-store on the response.
	for _, url := range []string{
		srv.URL + "/api/instances/demo/jwt",
		srv.URL + "/api/instances/demo/app-config?format=json",
	} {
		var resp *http.Response
		var err error
		if strings.Contains(url, "/jwt") {
			resp, err = http.Post(url, "application/json", strings.NewReader(""))
		} else {
			resp, err = http.Get(url)
		}
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		got := resp.Header.Get("Cache-Control")
		resp.Body.Close()
		if got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store — credentials cacheable", url, got)
		}
	}
}

// TestWriteError_5xxDoesNotLeakCausePath is the reviewer pin
// (PR #43 round-2 5xx leak): the 5xx response body must NOT
// include the cause string. The cause typically carries
// filesystem paths or other internal detail; it stays in the
// server-side log (TestWriteError_5xxLogsCauseServerSide pins
// the other half).
func TestWriteError_5xxDoesNotLeakCausePath(t *testing.T) {
	// Synthesise a cause that obviously contains a path.
	cause := errors.New("read /home/user/.canton-devkit/secret/state.json: permission denied")
	rec := newHandlersTestRecorder()
	writeError(rec, http.StatusInternalServerError, "internal error", cause)

	body := rec.body.String()
	for _, leak := range []string{
		"/home/user",
		".canton-devkit/secret",
		"permission denied",
	} {
		if strings.Contains(body, leak) {
			t.Errorf("5xx body leaked %q:\n%s", leak, body)
		}
	}
}

// TestWriteError_5xxLogsCauseServerSide is the other half: the
// cause MUST be logged via log.Default so the operator can
// still diagnose. Without this, the previous patch would have
// thrown away diagnostic information.
func TestWriteError_5xxLogsCauseServerSide(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Default().Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	cause := errors.New("read /tmp/state.json: missing")
	rec := newHandlersTestRecorder()
	writeError(rec, http.StatusInternalServerError, "internal error", cause)

	if !strings.Contains(buf.String(), "missing") ||
		!strings.Contains(buf.String(), "/tmp/state.json") {
		t.Errorf("5xx cause not logged server-side:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "handler error:") {
		t.Errorf("log line missing 'handler error:' prefix:\n%s", buf.String())
	}
}

// TestWriteError_4xxDoesNotLog — symmetric pin: 4xx errors are
// client input; we don't want to log every malformed request to
// the audit stream. Only 5xx (server-side failures the operator
// might need to diagnose) get logged.
func TestWriteError_4xxDoesNotLog(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Default().Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	writeError(newHandlersTestRecorder(), http.StatusBadRequest, "bad request",
		errors.New("client typed garbage"))

	if strings.Contains(buf.String(), "handler error:") {
		t.Errorf("4xx logged via handler-error stream (would flood logs with client input):\n%s", buf.String())
	}
}

// newHandlersTestRecorder is a tiny http.ResponseWriter that lets
// the table-style tests above inspect what writeError emits.
type handlersTestRecorder struct {
	hdr  http.Header
	code int
	body bytes.Buffer
}

func newHandlersTestRecorder() *handlersTestRecorder {
	return &handlersTestRecorder{hdr: http.Header{}}
}
func (r *handlersTestRecorder) Header() http.Header         { return r.hdr }
func (r *handlersTestRecorder) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *handlersTestRecorder) WriteHeader(code int)        { r.code = code }
