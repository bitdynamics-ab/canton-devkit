package v06

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestAdapterContract(t *testing.T) {
	a := New()

	if a.MajorVersion() != "0.6" {
		t.Errorf("MajorVersion = %q", a.MajorVersion())
	}

	cf := a.ComposeFiles()
	if len(cf) != 2 || cf[0] != "compose.yaml" || cf[1] != "resource-constraints.yaml" {
		t.Errorf("ComposeFiles = %v", cf)
	}

	if !a.SupportsAlphaProtocol() {
		t.Errorf("0.6.x must support alpha protocol")
	}

	env := a.OverlayEnv(splice.InstanceParams{
		Name:       "alice",
		Version:    splice.Version{Tag: "0.6.4", Major: "0.6"},
		ProjectDir: "/tmp/cache/splice-0.6.4",
	})
	for _, key := range []string{
		"LOCALNET_DIR", "LOCALNET_ENV_DIR", "IMAGE_TAG",
		"DOCKER_NETWORK", "PARTY_HINT", "COMPOSE_PROFILES",
		"ALPHA_PROTOCOL_VERSION_ENV", // 0.6.x specific
	} {
		if _, ok := env[key]; !ok {
			t.Errorf("OverlayEnv missing %q", key)
		}
	}
	if env["IMAGE_TAG"] != "0.6.4" {
		t.Errorf("IMAGE_TAG = %q", env["IMAGE_TAG"])
	}
	if !strings.Contains(env["ALPHA_PROTOCOL_VERSION_ENV"], "alpha-protocol-version.env") {
		t.Errorf("alpha env path looks wrong: %q", env["ALPHA_PROTOCOL_VERSION_ENV"])
	}
}

// The four tests below lock the adapter's constants against the upstream
// Splice 0.6.x compose project. If Splice changes container names,
// service names, port suffixes, or env-var names, one of these tests
// will fail before the regression reaches users. Drift this carefully —
// every value here is intentionally cross-referenced with
// cluster/compose/localnet/compose.yaml in canton-network/splice.

func TestOverlayEnv_FullValueSurface(t *testing.T) {
	a := New()
	p := splice.InstanceParams{
		Name:       "alice",
		Version:    splice.Version{Tag: "0.6.4", Major: "0.6"},
		ProjectDir: "/tmp/cache/splice-0.6.4",
	}
	env := a.OverlayEnv(p)

	wantExact := map[string]string{
		"LOCALNET_DIR":               "/tmp/cache/splice-0.6.4",
		"LOCALNET_ENV_DIR":           filepath.Join("/tmp/cache/splice-0.6.4", "env"),
		"IMAGE_TAG":                  "0.6.4",
		"DOCKER_NETWORK":             "alice",
		"PARTY_HINT":                 "alice-localparty-1",
		"COMPOSE_PROFILES":           "sv,app-provider,app-user,swagger-ui",
		"ALPHA_PROTOCOL_VERSION_ENV": filepath.Join("/tmp/cache/splice-0.6.4", "env", "alpha-protocol-version.env"),
	}
	for k, want := range wantExact {
		if got := env[k]; got != want {
			t.Errorf("OverlayEnv[%q] = %q, want %q", k, got, want)
		}
	}

	// Non-ephemeral: TEST_PORT must NOT be set, otherwise compose drops
	// every fixed canton/splice/postgres port binding.
	if _, ok := env["TEST_PORT"]; ok {
		t.Errorf("non-ephemeral OverlayEnv unexpectedly set TEST_PORT=%q", env["TEST_PORT"])
	}
}

func TestOverlayEnv_EphemeralFlipsTestPortAndApplyOverrides(t *testing.T) {
	a := New()
	p := splice.InstanceParams{
		Name:       "bob",
		Version:    splice.Version{Tag: "0.6.4", Major: "0.6"},
		ProjectDir: "/cache/splice",
		Ephemeral:  true,
		UIPortOverrides: map[string]int{
			"APP_USER_UI_PORT":     54000,
			"APP_PROVIDER_UI_PORT": 54001,
			"SV_UI_PORT":           54002,
			"SWAGGER_UI_PORT":      54003,
			"DB_PORT":              54004,
		},
	}
	env := a.OverlayEnv(p)

	// TEST_PORT must be present AND empty. Splice's compose substitution
	// `${TEST_PORT-DEFAULT}` returns DEFAULT only when TEST_PORT is unset;
	// set-but-empty collapses to "" which drops the host-side mapping.
	tp, ok := env["TEST_PORT"]
	if !ok {
		t.Fatalf("Ephemeral=true must set TEST_PORT")
	}
	if tp != "" {
		t.Errorf("Ephemeral TEST_PORT must be empty (Splice's set-but-empty trick); got %q", tp)
	}

	// Every UIPortOverride must surface as the corresponding env var.
	for k, want := range p.UIPortOverrides {
		if got := env[k]; got != "54000" && got != "54001" && got != "54002" && got != "54003" && got != "54004" {
			t.Errorf("env[%q] looks unwired: %q", k, got)
		}
		_ = want
	}
	if env["APP_USER_UI_PORT"] != "54000" {
		t.Errorf("APP_USER_UI_PORT = %q, want 54000", env["APP_USER_UI_PORT"])
	}
	if env["DB_PORT"] != "54004" {
		t.Errorf("DB_PORT = %q, want 54004", env["DB_PORT"])
	}
}

func TestEndpointMap_MatchesUpstreamHostPorts(t *testing.T) {
	// Cross-reference with Splice 0.6.x compose.yaml:
	//   nginx publishes ${SV_UI_PORT}:${SV_UI_PORT} (default 4000),
	//   ${APP_PROVIDER_UI_PORT}:… (3000), ${APP_USER_UI_PORT}:… (2000).
	//   swagger-ui publishes ${SWAGGER_UI_PORT}:8080 (default 9090).
	//   postgres publishes ${DB_PORT}:5432 (default 5432).
	want := map[string]int{
		"app_user_ui":     2000,
		"app_provider_ui": 3000,
		"sv_ui":           4000,
		"postgres":        5432,
		"swagger_ui":      9090,
	}
	got := New().EndpointMap()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EndpointMap drift:\n  got:  %v\n  want: %v", got, want)
	}
}

func TestEndpointServices_MatchUpstreamContainerPorts(t *testing.T) {
	// Cross-reference: nginx listens on 2000/3000/4000 (per-role
	// VirtualHost; see conf/nginx/*.conf), postgres on 5432,
	// swagger-ui on 8080.
	want := map[string]splice.ServicePort{
		"app_user_ui":     {Service: "nginx", ContainerPort: 2000},
		"app_provider_ui": {Service: "nginx", ContainerPort: 3000},
		"sv_ui":           {Service: "nginx", ContainerPort: 4000},
		"postgres":        {Service: "postgres", ContainerPort: 5432},
		"swagger_ui":      {Service: "swagger-ui", ContainerPort: 8080},
	}
	got := New().EndpointServices()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EndpointServices drift:\n  got:  %v\n  want: %v", got, want)
	}
}

func TestEnvFilesAndProfilesMatchUpstream(t *testing.T) {
	a := New()

	wantEnvFiles := []string{"compose.env", "env/common.env"}
	if !reflect.DeepEqual(a.EnvFiles(), wantEnvFiles) {
		t.Errorf("EnvFiles drift: got %v want %v", a.EnvFiles(), wantEnvFiles)
	}

	wantProfiles := []string{"sv", "app-provider", "app-user", "swagger-ui"}
	if !reflect.DeepEqual(a.Profiles(), wantProfiles) {
		t.Errorf("Profiles drift: got %v want %v", a.Profiles(), wantProfiles)
	}
}

// Compile-time check that Adapter satisfies the splice.Adapter contract.
var _ splice.Adapter = (*Adapter)(nil)
