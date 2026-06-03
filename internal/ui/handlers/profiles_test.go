package handlers

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
)

// TestAllowedProfiles pins the HTTP surface's accepted compose-profile
// set. The Create modal's "Token Standard V2" toggle sends
// profiles:["tokens-v2"], so the handler MUST accept it (CLI ↔ UI
// parity) — while still rejecting arbitrary strings.
func TestAllowedProfiles(t *testing.T) {
	for _, p := range []string{localnet.ObservabilityProfileName, localnet.TokensV2ProfileName} {
		if !allowedProfiles[p] {
			t.Errorf("profile %q must be accepted by the HTTP surface (UI toggle relies on it)", p)
		}
	}
	for _, p := range []string{"production", "", "../etc", "tokens_v2"} {
		if allowedProfiles[p] {
			t.Errorf("profile %q must NOT be accepted", p)
		}
	}
}
