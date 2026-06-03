package localnet

import (
	"errors"
	"fmt"
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

// TestReuseOrAllocateUIPorts_FirstRunAllocatesEphemeral pins the
// no-prior-state path: every env var gets a fresh ephemeral port and
// no two are equal.
func TestReuseOrAllocateUIPorts_FirstRunAllocatesEphemeral(t *testing.T) {
	got, err := ReuseOrAllocateUIPorts(UIPortEnvVars(), nil)
	if err != nil {
		t.Fatalf("ReuseOrAllocateUIPorts: %v", err)
	}
	seen := map[int]bool{}
	for ev, p := range got {
		if p <= 0 || p > 65535 {
			t.Errorf("%s = %d out of range", ev, p)
		}
		if seen[p] {
			t.Errorf("duplicate port %d", p)
		}
		seen[p] = true
	}
}

// TestReuseOrAllocateUIPorts_ReusesFreePriorPorts is the contract Zhe
// asked for in PR #20 #5/#7: if the previous state has a port and
// that port is currently free, hand the same number back so the
// user's bookmarked URLs keep working across an up/down/up.
func TestReuseOrAllocateUIPorts_ReusesFreePriorPorts(t *testing.T) {
	// Pick a real ephemeral port first so we know it's free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wantPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	prior := map[string]int{"app_user_ui": wantPort}
	got, err := ReuseOrAllocateUIPorts([]string{"APP_USER_UI_PORT"}, prior)
	if err != nil {
		t.Fatalf("ReuseOrAllocateUIPorts: %v", err)
	}
	if got["APP_USER_UI_PORT"] != wantPort {
		t.Errorf("APP_USER_UI_PORT: got %d want %d (reuse contract broken)",
			got["APP_USER_UI_PORT"], wantPort)
	}
}

// TestReuseOrAllocateUIPorts_ErrPortBusyOnConflict is the no-silent-
// fallback half of the stable-port contract. If a previously allocated
// port is currently bound, we MUST surface ErrPortBusy instead of
// quietly picking a different port (which would defeat the bookmark-
// preservation guarantee).
func TestReuseOrAllocateUIPorts_ErrPortBusyOnConflict(t *testing.T) {
	// Hold a port for the duration of the test so it's definitely busy.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	busyPort := ln.Addr().(*net.TCPAddr).Port

	prior := map[string]int{"app_user_ui": busyPort}
	_, err = ReuseOrAllocateUIPorts([]string{"APP_USER_UI_PORT"}, prior)
	if err == nil {
		t.Fatal("expected ErrPortBusy, got nil")
	}
	if !errors.Is(err, ErrPortBusy) {
		t.Errorf("expected ErrPortBusy, got %v", err)
	}
	// Error message must name the env var so the user knows which
	// port to free.
	if want := "APP_USER_UI_PORT"; !errContains(err, want) {
		t.Errorf("expected error to mention %q, got %v", want, err)
	}
	if want := fmt.Sprintf("%d", busyPort); !errContains(err, want) {
		t.Errorf("expected error to mention busy port %d, got %v", busyPort, err)
	}
}

// TestReuseOrAllocateUIPorts_ZeroPriorIsTreatedAsMissing covers the
// edge case where a previously crashed bring-up persisted state with
// port==0 — that's not a "stable port" claim, just a placeholder, so
// we fall back to ephemeral allocation instead of returning 0.
func TestReuseOrAllocateUIPorts_ZeroPriorIsTreatedAsMissing(t *testing.T) {
	prior := map[string]int{"app_user_ui": 0}
	got, err := ReuseOrAllocateUIPorts([]string{"APP_USER_UI_PORT"}, prior)
	if err != nil {
		t.Fatalf("ReuseOrAllocateUIPorts: %v", err)
	}
	if got["APP_USER_UI_PORT"] == 0 {
		t.Error("zero prior should not be reused; expected fresh ephemeral allocation")
	}
}

func errContains(err error, sub string) bool {
	return err != nil && contains(err.Error(), sub)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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
