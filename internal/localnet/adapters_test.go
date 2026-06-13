package localnet

import (
	"reflect"
	"testing"
)

// TestCoreServicesFor_Known pins the core-services resolver against a
// known version from the curated catalogue. The reconciler relies on the
// returned slice to distinguish "core stack missing" (failed) from
// "core stack running but degraded" (partial); if the resolver
// silently returns nil for a curated tag, evalStatus skips the check
// and the zombie-sidecar regression returns.
func TestCoreServicesFor_Known(t *testing.T) {
	got := CoreServicesFor("0.6.4")
	want := []string{"canton", "splice", "postgres", "nginx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CoreServicesFor(0.6.4) = %v, want %v", got, want)
	}
}

// TestCoreServicesFor_Unknown pins the back-compat contract: an
// unresolvable splice_version (renamed tag, removed entry, garbage
// input) must yield nil so evalStatus skips the core-services check
// rather than collapsing a perfectly healthy instance to `failed`.
func TestCoreServicesFor_Unknown(t *testing.T) {
	if got := CoreServicesFor("bogus-version-9.9.9"); got != nil {
		t.Fatalf("CoreServicesFor(bogus) = %v, want nil", got)
	}
}

// TestCoreServicesFor_VersionsAgree pins the hoist: 0.5.x and 0.6.x
// adapters both delegate to splice.CoreServices, so any drift between
// majors is impossible by construction. If a future major needs a
// different list, this test should be updated together with that
// adapter — not deleted.
func TestCoreServicesFor_VersionsAgree(t *testing.T) {
	v05 := CoreServicesFor("0.5.18")
	v06 := CoreServicesFor("0.6.4")
	if !reflect.DeepEqual(v05, v06) {
		t.Fatalf("core services drifted between majors: 0.5.18=%v 0.6.4=%v", v05, v06)
	}
}
