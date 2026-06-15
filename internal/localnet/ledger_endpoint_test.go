package localnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedLedgerInstance writes a registry state.json with the given
// participant ledger ports + credentials so ResolveLedgerEndpoint has
// something to read. Mirrors the seeding helpers in creds_test.go.
func seedLedgerInstance(t *testing.T, name string, ports map[string]int, creds map[string]registry.Credential) *registry.State {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.ProjectDir = t.TempDir()
	s.Ports = ports
	if creds != nil {
		s.Credentials = creds
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
	return s
}

// writeAuthEnv populates env/<role>-auth-on.env files under projectDir
// so the SignToken fallback path resolves. Same shape splice's
// LoadCredentialInputs reads.
func writeAuthEnv(t *testing.T, projectDir string) {
	t.Helper()
	envDir := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"sv-auth-on.env":           "AUTH_SV_AUDIENCE=https://example.com\nAUTH_SV_VALIDATOR_USER_NAME=sv-user",
		"app-provider-auth-on.env": "AUTH_APP_PROVIDER_AUDIENCE=https://example.com\nAUTH_APP_PROVIDER_VALIDATOR_USER_NAME=app-provider-user",
		"app-user-auth-on.env":     "AUTH_APP_USER_AUDIENCE=https://example.com\nAUTH_APP_USER_VALIDATOR_USER_NAME=app-user-user",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResolveLedgerEndpoint_CapturedCreds is the primary happy path
// with --endpoint omitted, the CLI must resolve the
// participant ledger port AND the per-role JWT from the registry —
// exactly what the Web UI already does — so the shipped skill
// examples (`dpm localnet contracts watch --name dev`) work.
func TestResolveLedgerEndpoint_CapturedCreds(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedLedgerInstance(t, "dev",
		map[string]int{
			"participant_ledger_app-user":     2901,
			"participant_ledger_app-provider": 3901,
		},
		map[string]registry.Credential{
			"app-user":     {Role: "app-user", JWT: "captured-app-user-jwt"},
			"app-provider": {Role: "app-provider", JWT: "captured-app-provider-jwt"},
		})

	// Default role → app-user.
	got, err := ResolveLedgerEndpoint("dev", "")
	if err != nil {
		t.Fatalf("ResolveLedgerEndpoint: %v", err)
	}
	if got.Endpoint != "localhost:2901" {
		t.Errorf("endpoint = %q, want localhost:2901", got.Endpoint)
	}
	if got.Token != "captured-app-user-jwt" {
		t.Errorf("token = %q, want captured-app-user-jwt", got.Token)
	}
	if got.Role != "app-user" {
		t.Errorf("role = %q, want app-user (default)", got.Role)
	}

	// Explicit role selects the matching port + JWT.
	got, err = ResolveLedgerEndpoint("dev", "app-provider")
	if err != nil {
		t.Fatalf("ResolveLedgerEndpoint(app-provider): %v", err)
	}
	if got.Endpoint != "localhost:3901" || got.Token != "captured-app-provider-jwt" {
		t.Errorf("app-provider resolved to %q / %q", got.Endpoint, got.Token)
	}
}

// TestResolveLedgerEndpoint_SignTokenFallback covers the case the V2
// alpha boot hits: creds capture raced, so state.Credentials is empty
// even though up succeeded. The resolver must sign a fresh per-role
// token from the cached project's env files rather than fail.
func TestResolveLedgerEndpoint_SignTokenFallback(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := seedLedgerInstance(t, "dev",
		map[string]int{"participant_ledger_app-user": 2901},
		nil) // no captured credentials
	writeAuthEnv(t, s.ProjectDir)

	got, err := ResolveLedgerEndpoint("dev", "app-user")
	if err != nil {
		t.Fatalf("ResolveLedgerEndpoint: %v", err)
	}
	if got.Endpoint != "localhost:2901" {
		t.Errorf("endpoint = %q, want localhost:2901", got.Endpoint)
	}
	// SignToken produces a compact JWS (eyJ...).
	if !strings.HasPrefix(got.Token, "eyJ") {
		t.Errorf("token = %q, want a signed JWT (eyJ...)", got.Token)
	}
}

// TestResolveLedgerEndpoint_DefaultInstance pins the `--name` help
// promise ("default: the only registered instance") that was
// implemented nowhere before.
func TestResolveLedgerEndpoint_DefaultInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedLedgerInstance(t, "solo",
		map[string]int{"participant_ledger_app-user": 2901},
		map[string]registry.Credential{"app-user": {Role: "app-user", JWT: "jwt"}})

	got, err := ResolveLedgerEndpoint("", "") // no --name
	if err != nil {
		t.Fatalf("ResolveLedgerEndpoint(default): %v", err)
	}
	if got.Instance != "solo" {
		t.Errorf("instance = %q, want solo (the only registered)", got.Instance)
	}
}

func TestResolveLedgerEndpoint_DefaultInstanceAmbiguous(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedLedgerInstance(t, "one",
		map[string]int{"participant_ledger_app-user": 2901},
		map[string]registry.Credential{"app-user": {JWT: "a"}})
	seedLedgerInstance(t, "two",
		map[string]int{"participant_ledger_app-user": 2902},
		map[string]registry.Credential{"app-user": {JWT: "b"}})

	_, err := ResolveLedgerEndpoint("", "")
	if err == nil {
		t.Fatal("expected an error when multiple instances are registered and --name is omitted")
	}
	if !strings.Contains(err.Error(), "multiple instances") {
		t.Errorf("error = %v, want a 'multiple instances' hint", err)
	}
}

func TestResolveLedgerEndpoint_NoInstances(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	_, err := ResolveLedgerEndpoint("", "")
	if err == nil {
		t.Fatal("expected an error when no instances are registered")
	}
	if !strings.Contains(err.Error(), "no LocalNet instances") {
		t.Errorf("error = %v, want a 'no instances' hint", err)
	}
}

// TestResolveLedgerEndpoint_MissingPort surfaces the same remedy the
// Web UI's PARTICIPANT_PORT_NOT_RECORDED gives: the instance pre-dates
// port capture, so we point the user at a restart (or --endpoint).
func TestResolveLedgerEndpoint_MissingPort(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedLedgerInstance(t, "dev",
		map[string]int{"app_user_ui": 4485}, // no participant_ledger_* port
		map[string]registry.Credential{"app-user": {JWT: "jwt"}})

	_, err := ResolveLedgerEndpoint("dev", "app-user")
	if err == nil {
		t.Fatal("expected an error when the participant ledger port is not recorded")
	}
	if !strings.Contains(err.Error(), "no recorded participant ledger port") {
		t.Errorf("error = %v, want a port-not-recorded hint", err)
	}
}

func TestResolveLedgerEndpoint_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	_, err := ResolveLedgerEndpoint("ghost", "app-user")
	if err == nil {
		t.Fatal("expected an error for an unregistered instance")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("error = %v, want 'not registered'", err)
	}
}
