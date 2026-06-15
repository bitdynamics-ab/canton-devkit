package localnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestComposeEnvForInstance_NilState verifies a clear error for nil state.
func TestComposeEnvForInstance_NilState(t *testing.T) {
	if _, err := ComposeEnvForInstance(nil, nil); err == nil {
		t.Error("expected error for nil state, got nil")
	}
}

// TestComposeEnvForInstance_MissingOverlay verifies that when
// overlay.env doesn't exist, ComposeEnvForInstance returns an error.
func TestComposeEnvForInstance_MissingOverlay(t *testing.T) {
	state := &registry.State{
		Name:          "test",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		DataDir:       t.TempDir(), // empty — no overlay.env
	}
	_, err := ComposeEnvForInstance(state, nil)
	if err == nil {
		t.Fatal("expected error when overlay.env missing, got nil")
	}
	if !strings.Contains(err.Error(), "overlay.env") {
		t.Errorf("error should mention overlay.env, got: %v", err)
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

	if got.Env != nil {
		t.Errorf("Env should be nil when overlay.env exists, got %d entries", len(got.Env))
	}

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

	last := got.EnvFiles[len(got.EnvFiles)-1]
	if !strings.HasSuffix(last, "override.env") {
		t.Errorf("last EnvFile = %q, want override.env suffix", last)
	}

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

// TestComposeContext_UsesOverlayFile verifies composeContext returns
// the overlay.env file and nil env.
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
		t.Errorf("env should be nil, got %d entries", len(env))
	}

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

// TestComposeContext_UncuratedInstanceResolves is the operability
// regression: an instance brought up with --allow-uncurated has a
// splice_version tag that ISN'T in the catalogue. composeContext must
// still reconstruct its env-file list (so down / logs / restart / clean
// work from a fresh shell) by inferring the adapter Major from the tag,
// rather than failing with ErrUncuratedTag the way catalogue-only
// Resolve would.
func TestComposeContext_UncuratedInstanceResolves(t *testing.T) {
	// Temp HOME so ResolveForOperation's resolved-cache read can't touch
	// the real ~/.canton-devkit.
	t.Setenv("HOME", t.TempDir())

	dataDir := t.TempDir()
	if _, err := WriteOverlayEnv(dataDir, map[string]string{
		"PARTY_HINT": "unc-hint",
		"IMAGE_TAG":  "0.6.99-rc.1",
	}); err != nil {
		t.Fatalf("WriteOverlayEnv: %v", err)
	}

	state := &registry.State{
		Name:          "unc",
		SpliceVersion: "0.6.99-rc.1", // NOT in the curated catalogue
		ProjectDir:    "/cache/splice-0.6.99-rc.1",
		DataDir:       dataDir,
	}

	_, envFiles, err := composeContext(state)
	if err != nil {
		t.Fatalf("composeContext for uncurated instance failed (the #68 bug): %v", err)
	}
	// The 0.6 adapter's env files plus the overlay must be present.
	found := false
	for _, f := range envFiles {
		if filepath.Base(f) == OverlayEnvFile {
			found = true
		}
	}
	if !found {
		t.Errorf("envFiles %v missing overlay.env for uncurated instance", envFiles)
	}
}

// TestComposeContext_ErrorWithoutOverlay verifies composeContext returns
// an error when overlay.env doesn't exist.
func TestComposeContext_ErrorWithoutOverlay(t *testing.T) {
	state := &registry.State{
		Name:          "test",
		SpliceVersion: "0.6.4",
		ProjectDir:    "/cache/splice-0.6.4",
		DataDir:       t.TempDir(),
	}

	_, _, err := composeContext(state)
	if err == nil {
		t.Fatal("expected error when overlay.env missing")
	}
}
