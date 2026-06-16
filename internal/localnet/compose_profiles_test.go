package localnet

import (
	"reflect"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestComposeProfiles_PrefersPersistedSet verifies composeProfiles
// returns the exact profile set recorded at `up` time. This is the set
// restart/pause/ps must replay; every Splice service is profile-gated,
// so dropping it targets zero services.
func TestComposeProfiles_PrefersPersistedSet(t *testing.T) {
	state := &registry.State{
		SpliceVersion: "0.6.4",
		Profiles:      []string{"sv", "app-provider", "app-user", "swagger-ui", "prometheus"},
	}
	got := composeProfiles(state)
	want := []string{"sv", "app-provider", "app-user", "swagger-ui", "prometheus"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("composeProfiles = %v, want %v (persisted set must win, incl. --profile opt-ins)", got, want)
	}
}

// TestComposeProfilesFor_DelegatesToComposeProfiles guards the exported
// accessor the CLI `down` command uses (cross-package) to pass the up-time
// profile set to its teardown runner — without it, `compose -f down` skips
// every profile-gated Splice service and falsely reports success. It must
// return exactly what composeProfiles does.
func TestComposeProfilesFor_DelegatesToComposeProfiles(t *testing.T) {
	for _, profiles := range [][]string{
		{"sv", "app-provider", "app-user", "swagger-ui", "observability"},
		nil, // pre-Profiles instance → adapter-base fallback
	} {
		state := &registry.State{SpliceVersion: "0.6.4", Profiles: profiles}
		if got, want := ComposeProfilesFor(state), composeProfiles(state); !reflect.DeepEqual(got, want) {
			t.Errorf("ComposeProfilesFor(%v) = %v, want %v (must delegate to composeProfiles)", profiles, got, want)
		}
	}
}

// TestComposeProfiles_FallsBackToAdapter verifies that for instances
// created before the Profiles field existed (state.Profiles == nil),
// composeProfiles re-derives the adapter's base profiles from the
// recorded Splice version so restart/pause/ps still target the core
// services. Without a non-empty set these subcommands would no-op.
func TestComposeProfiles_FallsBackToAdapter(t *testing.T) {
	state := &registry.State{
		SpliceVersion: "0.6.4",
		Profiles:      nil,
	}
	got := composeProfiles(state)
	if len(got) == 0 {
		t.Fatal("composeProfiles fell back to empty set for a pre-fix instance — " +
			"restart/pause/ps would target zero profile-gated services")
	}
	// The 0.6.x adapter gates the core Splice services behind these.
	want := []string{"sv", "app-provider", "app-user", "swagger-ui"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("composeProfiles fallback = %v, want adapter base profiles %v", got, want)
	}
}

// TestComposeProfiles_UnknownVersionDoesNotPanic guards the fallback
// path against an unresolvable recorded version — it must degrade to nil
// (best effort), never panic.
func TestComposeProfiles_UnknownVersionDoesNotPanic(t *testing.T) {
	state := &registry.State{SpliceVersion: "not-a-real-version", Profiles: nil}
	if got := composeProfiles(state); got != nil {
		t.Errorf("composeProfiles(unknown version) = %v, want nil", got)
	}
}
