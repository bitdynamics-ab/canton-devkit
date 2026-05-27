package localnet

import (
	"context"
	"fmt"
	"testing"
)

// TestParseComposePortLine — the docker-compose CLI emits a few
// shapes for `port` output across versions and dual-stack hosts.
// Pin every variant we've seen in the wild plus a few negatives
// so a regression in compose's output format surfaces here
// instead of as a silently-empty registry write.
func TestParseComposePortLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
		err  bool
	}{
		{"ipv4 happy", "0.0.0.0:53020\n", 53020, false},
		{"ipv6 happy", "[::]:53020\n", 53020, false},
		{"loopback ipv4", "127.0.0.1:54011\n", 54011, false},
		{"loopback ipv6", "[::1]:54011\n", 54011, false},
		// Dual-stack: compose emits two lines, one IPv4, one IPv6.
		// Either is correct; we just need the port.
		{"dual stack — ipv4 first", "0.0.0.0:53020\n[::]:53020\n", 53020, false},
		{"dual stack — ipv6 first", "[::]:53020\n0.0.0.0:53020\n", 53020, false},
		// Whitespace tolerance.
		{"trailing whitespace", "  0.0.0.0:53020  \n", 53020, false},
		// Empty / missing port → error.
		{"empty", "", 0, true},
		{"whitespace only", "  \n\n", 0, true},
		{"missing colon", "0.0.0.0\n", 0, true},
		{"non-numeric port", "0.0.0.0:abc\n", 0, true},
		{"port out of range — zero", "0.0.0.0:0\n", 0, true},
		{"port out of range — too high", "0.0.0.0:99999\n", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseComposePortLine(tc.in)
			if tc.err {
				if err == nil {
					t.Errorf("want error for %q, got %d", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for %q: %v", tc.in, err)
				return
			}
			if got != tc.want {
				t.Errorf("parseComposePortLine(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestCaptureCantonPorts_MissingPortsOmitted — the contract is
// "best-effort": a port that fails to query is OMITTED from the
// returned map, not stamped as 0. Tests use the composePortCmd
// seam to simulate per-port success / failure mix.
func TestCaptureCantonPorts_MissingPortsOmitted(t *testing.T) {
	// Save + restore the seam — package-level var.
	orig := composePortCmd
	t.Cleanup(func() { composePortCmd = orig })

	// Return a valid mapping only for the three Admin API ports.
	// The Ledger and JSON sets should be absent from the result.
	composePortCmd = func(_ context.Context, _ string, _ string, internal int) ([]byte, error) {
		switch internal {
		case 2902:
			return []byte("0.0.0.0:55001\n"), nil
		case 3902:
			return []byte("0.0.0.0:55002\n"), nil
		case 4902:
			return []byte("0.0.0.0:55003\n"), nil
		default:
			return nil, fmt.Errorf("no published port")
		}
	}

	got := CaptureCantonPorts(context.Background(), "test-project")
	want := map[string]int{
		"participant_admin_app_user":     55001,
		"participant_admin_app_provider": 55002,
		"participant_admin_sv":           55003,
	}
	if len(got) != len(want) {
		t.Fatalf("captured %d ports, want %d. got=%v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %d, want %d", k, got[k], v)
		}
	}
	// Negative: ledger keys must NOT appear (the contract).
	for _, key := range []string{
		"participant_ledger_app_user", "participant_ledger_app_provider",
		"participant_ledger_sv", "participant_json_app_user",
		"participant_json_app_provider", "participant_json_sv",
	} {
		if _, has := got[key]; has {
			t.Errorf("got[%q] should be absent (compose-port failed)", key)
		}
	}
}

// TestCaptureCantonPorts_AllNine — happy path: every port resolves,
// every canonical key shows up in the result.
func TestCaptureCantonPorts_AllNine(t *testing.T) {
	orig := composePortCmd
	t.Cleanup(func() { composePortCmd = orig })

	composePortCmd = func(_ context.Context, _ string, _ string, internal int) ([]byte, error) {
		// Use the internal as the host port for easy assertion.
		return []byte(fmt.Sprintf("0.0.0.0:%d\n", 50000+internal)), nil
	}

	got := CaptureCantonPorts(context.Background(), "p")
	if len(got) != len(CantonPortInternal) {
		t.Fatalf("got %d ports, want %d", len(got), len(CantonPortInternal))
	}
	for key, internal := range CantonPortInternal {
		want := 50000 + internal
		if got[key] != want {
			t.Errorf("got[%q] = %d, want %d", key, got[key], want)
		}
	}
}
