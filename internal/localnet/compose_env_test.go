package localnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestComposeEnvForInstance_CarriesSpliceVars is the regression pin
// for the observability-toggle bug: compose needs the adapter's
// overlay vars (PARTY_HINT, IMAGE_TAG, etc.) to interpolate the
// Splice base compose. ComposeEnvForInstance must provide them.
func TestComposeEnvForInstance_CarriesSpliceVars(t *testing.T) {
	state := &registry.State{
		Name:          "demo",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		DataDir:       t.TempDir(),
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

	// --env-file flags must be present.
	if len(got.EnvFiles) == 0 {
		t.Fatal("EnvFiles empty — compose would run without --env-file and fail interpolation")
	}

	// Fallback path (no overlay.env) puts vars in process env.
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
	// Inherited env must be present (PATH proves os.Environ() was kept).
	if env["PATH"] == "" {
		t.Error("PATH empty — os.Environ() was not inherited; docker would lose its lookup path")
	}
}

// TestComposeEnvForInstance_UnknownVersion surfaces a clear error
// rather than a nil-adapter panic when the persisted version isn't
// resolvable.
func TestComposeEnvForInstance_UnknownVersion(t *testing.T) {
	state := &registry.State{
		Name:          "x",
		SpliceVersion: "9.9.9-nope",
		ProjectDir:    "/tmp/x",
		DataDir:       t.TempDir(),
	}
	if _, err := ComposeEnvForInstance(state, nil); err == nil {
		t.Error("expected error for unresolvable splice version, got nil")
	}
}

// TestComposeEnvForInstance_UsesOverlayEnvFile verifies that when
// overlay.env exists on disk, ComposeEnvForInstance returns it in
// EnvFiles and sets Env to nil (inheriting process env).
func TestComposeEnvForInstance_UsesOverlayEnvFile(t *testing.T) {
	dataDir := t.TempDir()
	overlay := map[string]string{
		"PARTY_HINT":   "test-localparty-1",
		"IMAGE_TAG":    "0.6.4",
		"LOCALNET_DIR": "/cache/splice-0.6.4",
	}
	overlayPath, err := WriteOverlayEnv(dataDir, overlay)
	if err != nil {
		t.Fatalf("WriteOverlayEnv: %v", err)
	}

	state := &registry.State{
		Name:          "test",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		DataDir:       dataDir,
	}

	got, err := ComposeEnvForInstance(state, nil)
	if err != nil {
		t.Fatalf("ComposeEnvForInstance: %v", err)
	}

	// Env should be nil (no process env injection needed).
	if got.Env != nil {
		t.Errorf("Env should be nil when overlay.env exists, got %d entries", len(got.Env))
	}

	// EnvFiles should include the overlay.env path.
	found := false
	for _, f := range got.EnvFiles {
		if f == overlayPath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("EnvFiles %v missing overlay.env path %q", got.EnvFiles, overlayPath)
	}
}

// TestComposeEnvForInstance_OverlayWithPortOverrides verifies that
// uiPortOverrides generate an override.env file appended after overlay.env.
func TestComposeEnvForInstance_OverlayWithPortOverrides(t *testing.T) {
	dataDir := t.TempDir()
	_, err := WriteOverlayEnv(dataDir, map[string]string{
		"PARTY_HINT": "test-localparty-1",
		"IMAGE_TAG":  "0.6.4",
	})
	if err != nil {
		t.Fatalf("WriteOverlayEnv: %v", err)
	}

	state := &registry.State{
		Name:          "test",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		DataDir:       dataDir,
	}

	overrides := map[string]int{
		"PROMETHEUS_HOST_PORT": 0,
		"GRAFANA_HOST_PORT":    9090,
	}

	got, err := ComposeEnvForInstance(state, overrides)
	if err != nil {
		t.Fatalf("ComposeEnvForInstance: %v", err)
	}

	// Last env file should be override.env.
	last := got.EnvFiles[len(got.EnvFiles)-1]
	if !strings.HasSuffix(last, "override.env") {
		t.Errorf("last EnvFile = %q, want override.env suffix", last)
	}

	// override.env should contain the port values.
	data, err := os.ReadFile(last)
	if err != nil {
		t.Fatalf("read override.env: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "PROMETHEUS_HOST_PORT=0") {
		t.Errorf("override.env missing PROMETHEUS_HOST_PORT=0:\n%s", content)
	}
	if !strings.Contains(content, "GRAFANA_HOST_PORT=9090") {
		t.Errorf("override.env missing GRAFANA_HOST_PORT=9090:\n%s", content)
	}
}

// TestWriteOverlayEnv_RoundTrip verifies the file format is valid
// docker-compose env file syntax.
func TestWriteOverlayEnv_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	vars := map[string]string{
		"PARTY_HINT":   "demo-localparty-1",
		"IMAGE_TAG":    "0.6.4",
		"LOCALNET_DIR": "/some/path with spaces",
		"TEST_PORT":    "",
	}

	path, err := WriteOverlayEnv(dir, vars)
	if err != nil {
		t.Fatalf("WriteOverlayEnv: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}

	// Parse back.
	got := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '='); i >= 0 {
			got[line[:i]] = line[i+1:]
		}
	}

	for k, v := range vars {
		if got[k] != v {
			t.Errorf("roundtrip: got[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestComposeContext_UsesOverlayFile verifies composeContext prefers
// the overlay.env file and returns nil env.
func TestComposeContext_UsesOverlayFile(t *testing.T) {
	dataDir := t.TempDir()
	_, err := WriteOverlayEnv(dataDir, map[string]string{
		"PARTY_HINT": "ctx-test-hint",
		"IMAGE_TAG":  "0.6.4",
	})
	if err != nil {
		t.Fatalf("WriteOverlayEnv: %v", err)
	}

	state := &registry.State{
		Name:          "ctx-test",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		DataDir:       dataDir,
	}

	env, envFiles, err := composeContext(state)
	if err != nil {
		t.Fatalf("composeContext: %v", err)
	}

	if env != nil {
		t.Errorf("env should be nil when overlay.env exists, got %d entries", len(env))
	}

	// Should include overlay.env.
	found := false
	for _, f := range envFiles {
		if filepath.Base(f) == OverlayEnvFile {
			found = true
		}
	}
	if !found {
		t.Errorf("envFiles %v missing overlay.env", envFiles)
	}
}

// TestComposeContext_FallbackWithoutOverlayFile verifies that when
// overlay.env doesn't exist, composeContext falls back to adapter
// re-derivation with process env injection.
func TestComposeContext_FallbackWithoutOverlayFile(t *testing.T) {
	state := &registry.State{
		Name:          "legacy",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		DataDir:       t.TempDir(), // empty — no overlay.env
	}

	env, envFiles, err := composeContext(state)
	if err != nil {
		t.Fatalf("composeContext: %v", err)
	}

	// Fallback should inject into process env.
	if env == nil {
		t.Error("env should be non-nil in fallback mode (process env injection)")
	}

	envMap := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			envMap[kv[:i]] = kv[i+1:]
		}
	}
	if envMap["PARTY_HINT"] == "" {
		t.Error("PARTY_HINT empty in fallback mode")
	}
	if len(envFiles) == 0 {
		t.Error("envFiles empty in fallback mode")
	}
}
