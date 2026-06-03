package localnet

import (
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestComposeEnvForInstance_CarriesSpliceVars is the regression pin
// for the observability-toggle bug: the runtime toggle ran `docker
// compose up` without the adapter's OverlayEnv, so compose aborted
// with "required variable PARTY_HINT is missing a value" before
// prometheus/grafana could start. ComposeEnvForInstance must rebuild
// the same vars RunUp passes, derived purely from persisted state.
func TestComposeEnvForInstance_CarriesSpliceVars(t *testing.T) {
	state := &registry.State{
		Name:          "demo",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		Ports: map[string]int{
			"app_user_ui":     51077,
			"app_provider_ui": 51078,
			"sv_ui":           51079,
			"swagger_ui":      51080,
			"postgres":        51081,
		},
	}
	overrides := map[string]int{
		"APP_USER_UI_PORT":     state.Ports["app_user_ui"],
		"APP_PROVIDER_UI_PORT": state.Ports["app_provider_ui"],
		"SV_UI_PORT":           state.Ports["sv_ui"],
		"SWAGGER_UI_PORT":      state.Ports["swagger_ui"],
		"DB_PORT":              state.Ports["postgres"],
		"PROMETHEUS_HOST_PORT": 0,
		"GRAFANA_HOST_PORT":    0,
	}

	got, err := ComposeEnvForInstance(state, overrides)
	if err != nil {
		t.Fatalf("ComposeEnvForInstance: %v", err)
	}

	// --env-file flags must be the adapter's (relative to ProjectDir).
	if len(got.EnvFiles) == 0 {
		t.Fatal("EnvFiles empty — compose would run without --env-file and fail interpolation")
	}

	env := map[string]string{}
	for _, kv := range got.Env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}

	// The vars whose absence broke the toggle.
	want := map[string]string{
		"PARTY_HINT":           "demo-localparty-1",
		"LOCALNET_DIR":         "/cache/splice-0.6.4",
		"IMAGE_TAG":            "0.6.4",
		"DOCKER_NETWORK":       "demo",
		"APP_USER_UI_PORT":     "51077",
		"DB_PORT":              "51081",
		"PROMETHEUS_HOST_PORT": "0",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%q] = %q, want %q", k, env[k], v)
		}
	}
	if _, ok := env["COMPOSE_PROFILES"]; !ok {
		t.Error("COMPOSE_PROFILES missing — splice services wouldn't be selected")
	}
	// Inherited env must be present (PATH proves os.Environ() was kept).
	if env["PATH"] == "" {
		t.Error("PATH empty — os.Environ() was not inherited; docker would lose its lookup path")
	}
}

// TestComposeEnvForInstance_UnknownVersion surfaces a clear error
// rather than a nil-adapter panic when the persisted version isn't
// resolvable.
func TestComposeEnvForInstance_UnknownVersion(t *testing.T) {
	state := &registry.State{Name: "x", SpliceVersion: "9.9.9-nope", ProjectDir: "/tmp/x"}
	if _, err := ComposeEnvForInstance(state, nil); err == nil {
		t.Error("expected error for unresolvable splice version, got nil")
	}
}
