package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedInstance writes a minimal registry state.json so the handlers
// have something to read. Mirrors the helper in list_test.go on the
// p1-08 branch; duplicated here because that branch hasn't merged
// yet. TODO(BIT-146-merge): consolidate.
func seedInstance(t *testing.T, name, version string, ports map[string]int, status registry.Status) {
	t.Helper()
	s := registry.NewState(name, version)
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	s.Ports = ports
	s.Status = status
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// servingMux returns an http.ServeMux with the instance handlers
// mounted — the shape the production NewRouter assembles. Tests
// drive it via httptest.NewServer so we exercise the real Go 1.22
// pattern-matching path resolution (which differs subtly from
// raw HandlerFunc invocation).
func servingMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountInstances(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestList_EmptyRegistryReturnsEmptyArray pins the empty-state
// contract: with no instances registered, the API returns an
// empty `instances: []`, NOT `null` and NOT a 404. Frontends
// rendering an empty grid expect a JSON array.
func TestList_EmptyRegistryReturnsEmptyArray(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := servingMux(t)

	resp, err := http.Get(srv.URL + "/api/instances")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SchemaVersion != types.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, types.SchemaVersion)
	}
	if got.Instances == nil {
		t.Error("Instances = nil; want [] (frontend depends on array shape)")
	}
	if len(got.Instances) != 0 {
		t.Errorf("len(Instances) = %d, want 0", len(got.Instances))
	}
}

// TestList_RegisteredInstancesAppearSorted verifies the read path
// AND the sort: names come back in alphabetical order regardless
// of registration order. Browsers expect stable ordering between
// renders.
func TestList_RegisteredInstancesAppearSorted(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "zulu", "0.6.4",
		map[string]int{"app_user_ui": 6441}, registry.StatusRunning)
	seedInstance(t, "alpha", "0.6.4",
		map[string]int{"app_user_ui": 4441, "swagger_ui": 4487},
		registry.StatusRunning)
	srv := servingMux(t)

	resp, err := http.Get(srv.URL + "/api/instances")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var got types.ListResponse
	json.NewDecoder(resp.Body).Decode(&got)

	if len(got.Instances) != 2 {
		t.Fatalf("len = %d, want 2", len(got.Instances))
	}
	if got.Instances[0].Name != "alpha" || got.Instances[1].Name != "zulu" {
		t.Errorf("sort order: %v", []string{
			got.Instances[0].Name, got.Instances[1].Name,
		})
	}
	if got.Instances[0].Ports != "4441–4487" {
		t.Errorf("alpha.Ports = %q, want %q", got.Instances[0].Ports, "4441–4487")
	}
}

// TestDetail_ReturnsInstance is the happy path for /api/instances/{name}.
// We seed an instance, GET its detail endpoint, and assert the
// shape carries the registry fields without going through any
// docker call (Services stays nil because no live probe is wired
// in this PR — that lands when BIT-144 status.go merges).
func TestDetail_ReturnsInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 4441}, registry.StatusRunning)
	srv := servingMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/demo")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.Instance
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Name != "demo" {
		t.Errorf("Name = %q, want demo", got.Name)
	}
	if got.SpliceVersion != "0.6.4" {
		t.Errorf("SpliceVersion = %q, want 0.6.4", got.SpliceVersion)
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.SchemaVersion != types.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, types.SchemaVersion)
	}
}

// TestDetail_UnknownNameReturns404 — well-formed but not-registered
// name MUST return 404 (not 500, not 400). The frontend reads the
// status to decide whether to show "no instance" empty-state vs
// "something broke" toast.
func TestDetail_UnknownNameReturns404(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := servingMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/ghost")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	// Error body shape pin — frontend toasts read `error` field.
	var body errorBody
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Error == "" {
		t.Error("error body missing `error` field — frontend can't render toast")
	}
}

// TestDetail_InvalidNameReturns400 — malformed name (slash, path
// traversal attempt) is a 400 (user-error), not 404. Distinguishing
// these tells the frontend whether to surface "you typed garbage"
// vs "we don't have that instance".
func TestDetail_InvalidNameReturns400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := servingMux(t)

	// Slash in name triggers ValidateName failure. Use raw query
	// path that bypasses encoding so we hit the handler with the
	// actual character.
	resp, err := http.Get(srv.URL + "/api/instances/" + "bad..name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d for malformed name, want 400", resp.StatusCode)
	}
}

// TestList_PartialStateFileSurfacesWarning is the adversarial pin:
// a corrupt per-instance state.json on disk must NOT crash the
// list endpoint AND must surface in the response `warning` field.
// Mirrors the same regression class caught by
// TestList_PartialStateFileIsToleratedWithWarning on the p1-08
// branch.
func TestList_PartialStateFileSurfacesWarning(t *testing.T) {
	regRoot := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", regRoot)
	seedInstance(t, "good", "0.6.4",
		map[string]int{"app_user_ui": 4441}, registry.StatusRunning)
	seedInstance(t, "bad", "0.6.4",
		map[string]int{"app_user_ui": 5441}, registry.StatusRunning)
	// Corrupt one state file. registry.ReadIndex still sees both
	// (it reads index.json); the per-row registry.Read(bad) fails.
	badPath := regRoot + "/bad/state.json"
	if err := writeBytes(badPath, []byte("{not json")); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	srv := servingMux(t)
	resp, err := http.Get(srv.URL + "/api/instances")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (best-effort)", resp.StatusCode)
	}
	var got types.ListResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Warning == "" {
		t.Error("Warning empty after corrupt state.json — frontend can't surface degraded state")
	}
	if !strings.Contains(got.Warning, "bad") {
		t.Errorf("Warning should name the bad instance, got %q", got.Warning)
	}
	// Both rows still rendered.
	names := map[string]bool{}
	for _, in := range got.Instances {
		names[in.Name] = true
	}
	if !names["good"] || !names["bad"] {
		t.Errorf("both rows should appear, got %v", names)
	}
}

// writeBytes is a small helper for the corrupt-state-file test.
// Pulled out to keep the test body readable.
func writeBytes(path string, body []byte) error {
	return os.WriteFile(path, body, 0o600)
}
