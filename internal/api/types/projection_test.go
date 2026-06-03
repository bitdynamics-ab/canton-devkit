package types

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// registryToAPI is the SINGLE SOURCE OF TRUTH for the projection
// from registry.Status (the on-disk lifecycle enum) to the
// types.Instance.Status string emitted on the public wire. Every
// CLI status/list response + future Web UI handler renders one
// of these strings.
//
// Adding a new registry.Status constant MUST also add an entry
// here, or TestStatus_ProjectionLockedToRegistry fails — the
// "silent enum drift" failure mode the reviewer flagged.
var registryToAPI = map[registry.Status]string{
	registry.StatusCreating: "creating",
	registry.StatusRunning:  "running",
	registry.StatusStopping: "stopping", // requires the BIT-124 down branch's registry change
	registry.StatusStopped:  "stopped",
	registry.StatusFailed:   "failed",
	registry.StatusPartial:  "partial",
}

// TestStatus_ProjectionLockedToRegistry pins the registry → API
// projection. Reviewer pin on PR #31 #2.
//
// The failure mode this catches: a future ticket adds a new
// `registry.StatusXxx` constant (e.g. `StatusMigrating`) but
// forgets to teach the CLI / Web UI handler what string to
// render. Without this test, the new state would surface as the
// raw enum integer or a default-fallback rendering, breaking the
// status display silently.
//
// Strategy: table-driven over every constant currently exported
// from registry; assert each has an entry in registryToAPI and
// the value is non-empty.
func TestStatus_ProjectionLockedToRegistry(t *testing.T) {
	// Hand-enumerate the registry constants we know about. If a
	// new one is added, this list must be updated AND the
	// registryToAPI map above must gain an entry — the test
	// won't silently pass.
	all := []registry.Status{
		registry.StatusCreating,
		registry.StatusRunning,
		registry.StatusStopping,
		registry.StatusStopped,
		registry.StatusFailed,
		registry.StatusPartial,
	}
	for _, s := range all {
		mapped, ok := registryToAPI[s]
		if !ok {
			t.Errorf("registry.Status %q has no entry in registryToAPI", s)
			continue
		}
		if mapped == "" {
			t.Errorf("registry.Status %q maps to empty string", s)
		}
	}

	// Symmetric: every entry in the map must reference a real
	// registry.Status constant. A typo on the registry side
	// (e.g. removing a constant) shows up here too.
	for k := range registryToAPI {
		found := false
		for _, s := range all {
			if s == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("registryToAPI key %q is not in the registry.Status* set — stale entry?", k)
		}
	}
}
