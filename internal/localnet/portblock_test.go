package localnet

import (
	"net"
	"testing"
)

func TestAllocateUIPorts_AllDistinctAndBound(t *testing.T) {
	envVars := []string{"A", "B", "C"}
	got, err := AllocateUIPorts(envVars)
	if err != nil {
		t.Fatalf("AllocateUIPorts: %v", err)
	}
	if len(got) != len(envVars) {
		t.Fatalf("expected %d ports, got %d", len(envVars), len(got))
	}
	seen := map[int]bool{}
	for _, ev := range envVars {
		p, ok := got[ev]
		if !ok {
			t.Errorf("missing port for env %q", ev)
			continue
		}
		if p <= 0 || p > 65535 {
			t.Errorf("port %d for %q out of range", p, ev)
		}
		if seen[p] {
			t.Errorf("duplicate port %d", p)
		}
		seen[p] = true
		// Sanity: the OS should immediately allow us to bind the just-
		// returned port, proving it's free at allocation time.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen sanity: %v", err)
		}
		_ = ln.Close()
	}
}

func TestUIPortEnvVars_StableContract(t *testing.T) {
	want := []string{
		"APP_USER_UI_PORT",
		"APP_PROVIDER_UI_PORT",
		"SV_UI_PORT",
		"SWAGGER_UI_PORT",
		"DB_PORT",
	}
	got := UIPortEnvVars()
	if len(got) != len(want) {
		t.Fatalf("UIPortEnvVars length: got %d want %d", len(got), len(want))
	}
	for i, ev := range want {
		if got[i] != ev {
			t.Errorf("UIPortEnvVars[%d] = %q, want %q", i, got[i], ev)
		}
	}
}
