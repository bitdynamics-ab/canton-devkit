package localnet

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// TestSpliceMemoryFloors_NeverBelowDockerGate pins the invariant that
// internal/docker/threshold_parity_test.go relies on: the per-version
// splice memory floors may REPLACE the literal docker.DefaultMinMemoryBytes
// in `up`/`doctor`/the UI preflight handler ONLY because they can only
// RAISE the floor, never lower it. If a catalogue edit ever set
// MinMemoryFor(v) below the docker gate, the preflight surfaces would
// silently admit a host the doctor gate would reject.
//
// This lives in internal/localnet because it is the one package that
// already imports both internal/splice and internal/docker, so it can
// compare the two without adding a new import edge.
func TestSpliceMemoryFloors_NeverBelowDockerGate(t *testing.T) {
	if len(splice.SupportedVersions) == 0 {
		t.Fatal("splice.SupportedVersions is empty — catalogue failed to load")
	}
	for tag, v := range splice.SupportedVersions {
		min := splice.MinMemoryFor(v)
		if min < docker.DefaultMinMemoryBytes {
			t.Errorf("splice.MinMemoryFor(%q) = %d is BELOW docker.DefaultMinMemoryBytes = %d — "+
				"the preflight surfaces would admit a host the doctor gate rejects",
				tag, min, docker.DefaultMinMemoryBytes)
		}
		// The recommended floor must also dominate the hard minimum, or
		// the "recommended" hint would advise less than the gate requires.
		if rec := splice.RecommendedMemoryFor(v); rec < min {
			t.Errorf("splice.RecommendedMemoryFor(%q) = %d < MinMemoryFor = %d", tag, rec, min)
		}
	}
}
