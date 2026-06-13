package httpsec

import "testing"

// TestIsLoopbackHost is the DNS-rebinding allowlist pin: the Host
// header host-part must be a loopback name. evil.example.com and a
// LAN IP (the rebinding target's *name*, not 127.0.0.1) must be
// rejected even though the connection itself terminates on loopback.
func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		// Loopback — accepted, with and without ports.
		{"127.0.0.1", true},
		{"127.0.0.1:7777", true},
		{"127.0.0.53", true}, // anywhere in 127.0.0.0/8
		{"localhost", true},
		{"localhost:7777", true},
		{"LocalHost:7777", true}, // case-insensitive name
		{"::1", true},
		{"[::1]", true},
		{"[::1]:7777", true},
		// Not loopback — rejected. These are the rebinding vector:
		// the browser sends the attacker's hostname as Host.
		{"evil.example.com", false},
		{"evil.example.com:7777", false},
		{"169.254.13.37:7777", false}, // link-local LAN IP
		{"10.0.0.5:7777", false},
		{"0.0.0.0:7777", false}, // wildcard is not loopback
		{"", false},             // missing Host fails closed
		{"   ", false},
	}
	for _, c := range cases {
		if got := IsLoopbackHost(c.host); got != c.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestIsLoopbackHost_ExtraAllowed pins the --allow-non-loopback
// escape hatch: a host the operator explicitly bound to is accepted
// even though it isn't loopback, while OTHER non-loopback hosts stay
// rejected (the allowlist is exact, not "anything goes").
func TestIsLoopbackHost_ExtraAllowed(t *testing.T) {
	if !IsLoopbackHost("10.0.0.5:7777", "10.0.0.5") {
		t.Error("explicitly-allowed bind host 10.0.0.5 should pass")
	}
	if !IsLoopbackHost("devbox.lan:7777", "devbox.lan") {
		t.Error("explicitly-allowed bind hostname should pass (case-insensitive)")
	}
	if IsLoopbackHost("evil.example.com:7777", "10.0.0.5") {
		t.Error("a non-allowed host must stay rejected even with an allowlist entry")
	}
	// Loopback still passes regardless of extra entries.
	if !IsLoopbackHost("127.0.0.1:7777", "10.0.0.5") {
		t.Error("loopback must always pass")
	}
	// Blank allowlist entries are ignored, not treated as a wildcard.
	if IsLoopbackHost("evil.example.com", "", "   ") {
		t.Error("blank allowlist entries must not allow arbitrary hosts")
	}
}

// TestCheckOriginAgainstHost covers the cross-origin matrix the
// package-ui middleware and the per-instance SSE handler both rely on.
func TestCheckOriginAgainstHost(t *testing.T) {
	cases := []struct {
		name                  string
		origin, referer, host string
		wantErr               error
	}{
		{"match", "http://127.0.0.1:7777", "", "127.0.0.1:7777", nil},
		{"match via referer", "", "http://127.0.0.1:7777/x", "127.0.0.1:7777", nil},
		{"origin wins over referer", "http://127.0.0.1:7777", "http://evil.example.com", "127.0.0.1:7777", nil},
		{"cross origin", "https://evil.example.com", "", "127.0.0.1:7777", ErrOriginMismatch},
		{"missing host", "http://127.0.0.1:7777", "", "", ErrMissingHost},
		{"missing origin and referer", "", "", "127.0.0.1:7777", ErrMissingOrigin},
		{"malformed origin", "not-a-url", "", "127.0.0.1:7777", ErrMalformedOrigin},
		// DNS-rebinding shape: Origin == Host == attacker. The Origin
		// check PASSES here by design — IsLoopbackHost is what stops
		// it. This pins that CheckOriginAgainstHost alone is not the
		// rebinding defence.
		{"rebinding passes origin check", "http://evil.example.com:7777", "", "evil.example.com:7777", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckOriginAgainstHost(c.origin, c.referer, c.host)
			if err != c.wantErr {
				t.Errorf("CheckOriginAgainstHost(%q,%q,%q) = %v, want %v",
					c.origin, c.referer, c.host, err, c.wantErr)
			}
		})
	}
}

// TestStripOriginToHost pins URL host extraction.
func TestStripOriginToHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:7777/foo", "127.0.0.1:7777"},
		{"https://evil.example.com", "evil.example.com"},
		{"http://h:8080/a?b=c", "h:8080"},
		{"http://h?q", "h"},
		{"no-scheme-here", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := StripOriginToHost(c.in); got != c.want {
			t.Errorf("StripOriginToHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHostsMatch pins the implicit-port tolerance rules.
func TestHostsMatch(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"127.0.0.1:7777", "127.0.0.1:7777", true},
		{"127.0.0.1", "127.0.0.1:7777", true},
		{"127.0.0.1:7777", "127.0.0.1", true},
		{"127.0.0.1", "evil.example.com", false},
		{"127.0.0.1:7778", "127.0.0.1:7777", false},
	}
	for _, c := range cases {
		if got := HostsMatch(c.a, c.b); got != c.want {
			t.Errorf("HostsMatch(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
