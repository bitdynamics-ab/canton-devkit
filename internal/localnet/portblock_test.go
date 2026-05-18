package localnet

import (
	"net"
	"testing"
)

func TestChoosePortStrategy_FixedWhenAllFree(t *testing.T) {
	// Use ports we just freed; very likely to remain free for a few ms.
	ln1 := mustListen(t)
	p1 := portOf(t, ln1.Addr().String())
	_ = ln1.Close()

	ln2 := mustListen(t)
	p2 := portOf(t, ln2.Addr().String())
	_ = ln2.Close()

	got := ChoosePortStrategy([]int{p1, p2}, false)
	if got != PortFixed {
		t.Errorf("expected PortFixed for free ports, got %s", got)
	}
}

func TestChoosePortStrategy_EphemeralWhenAnyBusy(t *testing.T) {
	ln := mustListen(t)
	defer func() { _ = ln.Close() }()
	busy := portOf(t, ln.Addr().String())

	got := ChoosePortStrategy([]int{busy}, false)
	if got != PortEphemeral {
		t.Errorf("expected PortEphemeral when port busy, got %s", got)
	}
}

func TestChoosePortStrategy_EphemeralWhenFixedTaken(t *testing.T) {
	got := ChoosePortStrategy([]int{1234567}, true)
	if got != PortEphemeral {
		t.Errorf("expected PortEphemeral when caller says fixedTaken, got %s", got)
	}
}

func TestPortStrategyString(t *testing.T) {
	if PortFixed.String() != "fixed" || PortEphemeral.String() != "ephemeral" {
		t.Errorf("PortStrategy strings drifted: %s, %s", PortFixed, PortEphemeral)
	}
	if PortStrategy(99).String() != "unknown" {
		t.Errorf("unknown PortStrategy should print 'unknown'")
	}
}

// --- helpers (also reused by other tests in this package) ---

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	var p int
	if _, err := mustScan(port, &p); err != nil {
		t.Fatalf("scan port %q: %v", port, err)
	}
	return p
}

func mustScan(s string, p *int) (int, error) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errBadPort
		}
		n = n*10 + int(c-'0')
	}
	*p = n
	return 1, nil
}

var errBadPort = badPortErr{}

type badPortErr struct{}

func (badPortErr) Error() string { return "non-numeric port" }
