package token

import "testing"

// TestValidatePartyID guards the issuer-party check RunCreate now applies
// (previously it accepted any non-empty string).
func TestValidatePartyID(t *testing.T) {
	good := []string{"alice", "alice::1220abcd", "app_provider", "sv-1", "Bob#2"}
	for _, s := range good {
		if err := validatePartyID("issuer", s); err != nil {
			t.Errorf("validatePartyID(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", " ", "a b", "alice\n", "💀", "::nohint"}
	for _, s := range bad {
		if err := validatePartyID("issuer", s); err == nil {
			t.Errorf("validatePartyID(%q) = nil, want error", s)
		}
	}
}
