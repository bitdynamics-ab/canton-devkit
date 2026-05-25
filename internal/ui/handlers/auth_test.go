package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// authMux returns a test server with both instances and auth
// handlers mounted — auth depends on the instance existing.
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

	resp, err := http.Post(srv.URL+"/api/instances/demo/jwt",
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
