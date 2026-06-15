package localnet

import (
	"path/filepath"
	"testing"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedEnvState writes a running instance with the full endpoint /
// credential / party set the env builder is supposed to surface.
func seedEnvState(t *testing.T, name string) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = "/test/" + name
	s.Status = registry.StatusRunning
	s.Ports = map[string]int{
		"app_user_ui":                     4485,
		"sv_ui":                           4480,
		"postgres":                        5432,
		"participant_ledger_app-user":     2901,
		"participant_admin_app-user":      2902,
		"participant_json_app-user":       2975,
		"participant_ledger_app-provider": 3901,
	}
	s.Credentials = map[string]registry.Credential{
		"app-user": {
			Role:     "app-user",
			User:     "app-user-ledger-api-user",
			Audience: "https://canton.network.global",
			JWT:      "eyJ.autoken.sig",
		},
	}
	s.Parties = map[string]registry.PartyRef{
		"app-user": {
			Alias:   "app-user",
			PartyID: "app-user::1220deadbeef",
			Role:    "app-user",
		},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestBuildEnvExport_IncludesParticipantPorts pins the core fix: every
// port in state.Ports — including the participant Ledger/Admin/JSON
// API ports — is exported as CANTON_<KEY>_PORT. These are the
// endpoints an external dApp needs, and the divergent UI export used
// to hide them.
func TestBuildEnvExport_IncludesParticipantPorts(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvState(t, "demo")

	ex, err := BuildEnvExport("demo", false)
	if err != nil {
		t.Fatalf("BuildEnvExport: %v", err)
	}
	want := map[string]string{
		"CANTON_INSTANCE":                             "demo",
		"CANTON_SPLICE_VERSION":                       "0.6.4",
		"CANTON_AUTH_FILE":                            filepath.Join("/test/demo", "auth.json"),
		"CANTON_APP_USER_UI_PORT":                     "4485",
		"CANTON_SV_UI_PORT":                           "4480",
		"CANTON_POSTGRES_PORT":                        "5432",
		"CANTON_PARTICIPANT_LEDGER_APP_USER_PORT":     "2901",
		"CANTON_PARTICIPANT_ADMIN_APP_USER_PORT":      "2902",
		"CANTON_PARTICIPANT_JSON_APP_USER_PORT":       "2975",
		"CANTON_PARTICIPANT_LEDGER_APP_PROVIDER_PORT": "3901",
	}
	for k, v := range want {
		if ex.Vars[k] != v {
			t.Errorf("Vars[%q] = %q, want %q", k, ex.Vars[k], v)
		}
	}
}

// TestBuildEnvExport_ScanUIURL pins that the scan UI is surfaced under
// an explicit, self-describing key carrying the scan.localhost vhost
// hint — the proposal lists "scan UI" as a distinct value.
func TestBuildEnvExport_ScanUIURL(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvState(t, "demo")

	ex, err := BuildEnvExport("demo", false)
	if err != nil {
		t.Fatalf("BuildEnvExport: %v", err)
	}
	if got := ex.Vars["CANTON_SCAN_UI_URL"]; got != "http://scan.localhost:4480" {
		t.Errorf("CANTON_SCAN_UI_URL = %q, want http://scan.localhost:4480", got)
	}
}

// TestBuildEnvExport_NoScanUIWhenPortMissing — the scan URL is only
// emitted when sv_ui was actually captured; an instance that came up
// without the SV profile must not get a dangling CANTON_SCAN_UI_URL.
func TestBuildEnvExport_NoScanUIWhenPortMissing(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("noscan", "0.6.4")
	s.ProjectDir = t.TempDir()
	s.DataDir = "/test/noscan"
	s.Status = registry.StatusRunning
	s.Ports = map[string]int{"app_user_ui": 4485}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ex, err := BuildEnvExport("noscan", false)
	if err != nil {
		t.Fatalf("BuildEnvExport: %v", err)
	}
	if _, ok := ex.Vars["CANTON_SCAN_UI_URL"]; ok {
		t.Errorf("CANTON_SCAN_UI_URL present without sv_ui port: %q", ex.Vars["CANTON_SCAN_UI_URL"])
	}
}

// TestBuildEnvExport_PartyIDsDistinctFromUser pins the party-id fix: party
// ids come from state.Parties (real on-ledger ids), NOT from the
// credential User (a ledger-api user name). Conflating the two is the
// defect the old UI partiesFromCredentials shipped.
func TestBuildEnvExport_PartyIDsDistinctFromUser(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvState(t, "demo")

	ex, err := BuildEnvExport("demo", false)
	if err != nil {
		t.Fatalf("BuildEnvExport: %v", err)
	}
	if got := ex.Vars["CANTON_APP_USER_PARTY"]; got != "app-user::1220deadbeef" {
		t.Errorf("CANTON_APP_USER_PARTY = %q, want the real party id", got)
	}
	// The ledger-api user name still rides on _USER and must NOT be
	// reused as the party.
	if got := ex.Vars["CANTON_APP_USER_USER"]; got != "app-user-ledger-api-user" {
		t.Errorf("CANTON_APP_USER_USER = %q, want the credential user name", got)
	}
	if ex.Vars["CANTON_APP_USER_PARTY"] == ex.Vars["CANTON_APP_USER_USER"] {
		t.Error("party id conflated with user name — #17 regression")
	}
}

// TestBuildEnvExport_PartySkippedWhenEmpty — a PartyRef with no
// PartyID (not yet scanned) must not emit a blank CANTON_<ROLE>_PARTY.
func TestBuildEnvExport_PartySkippedWhenEmpty(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("blank", "0.6.4")
	s.ProjectDir = t.TempDir()
	s.DataDir = "/test/blank"
	s.Status = registry.StatusRunning
	s.Parties = map[string]registry.PartyRef{
		"sv": {Alias: "sv", PartyID: "", Role: "sv"},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ex, err := BuildEnvExport("blank", false)
	if err != nil {
		t.Fatalf("BuildEnvExport: %v", err)
	}
	if _, ok := ex.Vars["CANTON_SV_PARTY"]; ok {
		t.Errorf("emitted CANTON_SV_PARTY for an empty PartyID")
	}
}

// TestBuildEnvExport_JWTRedaction pins the default-redacted posture +
// the opt-in path. The dev-only signing secret must never appear
// unless includeJWT is explicitly set.
func TestBuildEnvExport_JWTRedaction(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvState(t, "demo")

	redacted, err := BuildEnvExport("demo", false)
	if err != nil {
		t.Fatalf("BuildEnvExport(redacted): %v", err)
	}
	if redacted.Vars["CANTON_APP_USER_JWT"] != EnvJWTRedaction {
		t.Errorf("default JWT = %q, want %q", redacted.Vars["CANTON_APP_USER_JWT"], EnvJWTRedaction)
	}

	raw, err := BuildEnvExport("demo", true)
	if err != nil {
		t.Fatalf("BuildEnvExport(raw): %v", err)
	}
	if raw.Vars["CANTON_APP_USER_JWT"] != "eyJ.autoken.sig" {
		t.Errorf("raw JWT = %q, want the captured token", raw.Vars["CANTON_APP_USER_JWT"])
	}
}

// TestBuildEnvExport_NotFound surfaces registry.ErrNotFound so callers
// can map it to a clean 404 / user error.
func TestBuildEnvExport_NotFound(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	if _, err := BuildEnvExport("ghost", false); err == nil {
		t.Fatal("expected error for unknown instance")
	}
}

// TestBuildEnvExport_SchemaVersion ensures the shared export carries
// the api/types schema version both surfaces validate against.
func TestBuildEnvExport_SchemaVersion(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvState(t, "demo")

	ex, err := BuildEnvExport("demo", false)
	if err != nil {
		t.Fatalf("BuildEnvExport: %v", err)
	}
	if ex.SchemaVersion != apitypes.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", ex.SchemaVersion, apitypes.SchemaVersion)
	}
	if ex.Instance != "demo" {
		t.Errorf("Instance = %q, want demo", ex.Instance)
	}
}

func TestPortEnvKeyAndCredPrefix(t *testing.T) {
	portCases := map[string]string{
		"app_user_ui":                 "CANTON_APP_USER_UI_PORT",
		"app-provider-ui":             "CANTON_APP_PROVIDER_UI_PORT",
		"participant_ledger_app-user": "CANTON_PARTICIPANT_LEDGER_APP_USER_PORT",
		"SV":                          "CANTON_SV_PORT",
	}
	for in, want := range portCases {
		if got := PortEnvKey(in); got != want {
			t.Errorf("PortEnvKey(%q) = %q, want %q", in, got, want)
		}
	}
	credCases := map[string]string{
		"sv":           "CANTON_SV",
		"app-user":     "CANTON_APP_USER",
		"app-provider": "CANTON_APP_PROVIDER",
	}
	for in, want := range credCases {
		if got := CredEnvKeyPrefix(in); got != want {
			t.Errorf("CredEnvKeyPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
