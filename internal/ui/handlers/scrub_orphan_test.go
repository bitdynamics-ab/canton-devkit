package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TestScrub_OrphanIndexEntry pins the user-reported repair flow:
// when a previous interrupted teardown (or manual filesystem wipe)
// leaves an entry in index.json pointing at a missing state.json,
// the Web UI shows "instance not registered" on the detail page
// and "unreadable per-instance state files: NAME" in the list
// warning. Clicking Remove on the orphan row must scrub the index
// entry so the list reflects truth on next refresh.
//
// Before the fix, the DELETE handler 404'd at the registry.Read
// step and left the orphan in place — the user had to hand-edit
// ~/.canton-devkit/localnet/index.json to recover.
func TestScrub_OrphanIndexEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", root)

	// Set up an orphan: write a valid instance, then remove its
	// directory while leaving the index entry behind.
	seedInstance(t, "ghost", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusStopped)
	dir := filepath.Join(root, "ghost")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if _, err := registry.Read("ghost"); err == nil {
		t.Fatalf("test setup: ghost should be unreadable after wipe")
	}
	idx, err := registry.ReadIndex()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	found := false
	for _, e := range idx.Entries {
		if e.Name == "ghost" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("test setup: ghost should still be in index")
	}

	// Issue the DELETE.
	hub := stream.New()
	defer hub.Close()
	mux := http.NewServeMux()
	MountInstances(mux, hub)
	req := httptest.NewRequest(http.MethodDelete, "/api/instances/ghost", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// Index entry must be gone.
	idx, err = registry.ReadIndex()
	if err != nil {
		t.Fatalf("post-scrub read index: %v", err)
	}
	for _, e := range idx.Entries {
		if e.Name == "ghost" {
			t.Errorf("orphan entry %q still in index after DELETE", e.Name)
		}
	}
}

// TestScrub_TrulyUnknownStill404 pins the negative half of the
// same contract: a name that's neither on disk nor in the index
// is a real 404, not a no-op 204. Distinguishes legitimate "user
// typoed an instance" from the orphan-repair case.
func TestScrub_TrulyUnknownStill404(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	hub := stream.New()
	defer hub.Close()
	mux := http.NewServeMux()
	MountInstances(mux, hub)

	req := httptest.NewRequest(http.MethodDelete, "/api/instances/never-existed", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
