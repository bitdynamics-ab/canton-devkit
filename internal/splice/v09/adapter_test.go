package v09

import "testing"

func TestMajorVersion(t *testing.T) {
	if got := New().MajorVersion(); got != "0.9" {
		t.Fatalf("MajorVersion() = %q, want 0.9", got)
	}
}

func TestDelegatesComposeSurface(t *testing.T) {
	a := New()
	if len(a.ComposeFiles()) == 0 || len(a.EnvFiles()) == 0 || len(a.Profiles()) == 0 {
		t.Fatal("0.9 adapter must expose the same compose surface as 0.6")
	}
	if !a.SupportsAlphaProtocol() {
		t.Fatal("0.9 must keep alpha-protocol support from 0.6")
	}
}
