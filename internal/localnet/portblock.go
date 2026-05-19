package localnet

import (
	"fmt"
	"net"
)

// PortStrategy describes how an instance's host ports are assigned.
type PortStrategy int

const (
	// PortFixed publishes each service on the upstream default host port
	// (2000/3000/4000/5432/9090 + participant suffixes). Used when no
	// other instance holds those ports.
	PortFixed PortStrategy = iota

	// PortEphemeral sets TEST_PORT=1 in the compose env. Splice's
	// compose.yaml interprets that as "drop the fixed `host:container`
	// binding" so Docker assigns a random ephemeral host port to every
	// published service. Used when the default ports are taken.
	//
	// The drawback: we can't compute the endpoint URLs from the static
	// EndpointMap — we have to query `docker compose port <svc>
	// <container-port>` after `up` finishes.
	PortEphemeral
)

// String makes PortStrategy printable for logs / debug output.
func (s PortStrategy) String() string {
	switch s {
	case PortFixed:
		return "fixed"
	case PortEphemeral:
		return "ephemeral"
	}
	return "unknown"
}

// ChoosePortStrategy decides between fixed and ephemeral ports. Tries to
// bind every defaultPort on 127.0.0.1; if all succeed, fixed wins. Any
// collision (or any prior instance already taking the fixed range)
// forces ephemeral.
func ChoosePortStrategy(defaultPorts []int, fixedTaken bool) PortStrategy {
	if fixedTaken {
		return PortEphemeral
	}
	for _, p := range defaultPorts {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return PortEphemeral
		}
		_ = ln.Close()
	}
	return PortFixed
}

// AllocateUIPorts picks a free OS-assigned host port for each
// (envVar → defaultPort) entry. Used in ephemeral mode where the
// upstream compose project expects a specific env var to name the host
// port for each UI/postgres service.
//
// Implementation detail: we bind on :0 to let the kernel choose, then
// close immediately. There's a small TOCTOU race window before docker
// compose re-binds the port; in practice the kernel doesn't reissue the
// same ephemeral port within milliseconds, so this works reliably for
// dev tooling.
func AllocateUIPorts(envVars []string) (map[string]int, error) {
	out := make(map[string]int, len(envVars))
	for _, ev := range envVars {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("allocate port for %s: %w", ev, err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		// addr looks like "127.0.0.1:54321"; split off the port.
		var port int
		if i := lastByte(addr, ':'); i >= 0 {
			if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err != nil {
				return nil, fmt.Errorf("parse allocated port %q: %w", addr, err)
			}
		}
		out[ev] = port
	}
	return out, nil
}

func lastByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// UIPortEnvVars enumerates the env-var names the Splice LocalNet compose
// substitutes for host ports of non-canton services. Names are stable
// across 0.5.x and 0.6.x (confirmed via the adapter design research).
func UIPortEnvVars() []string {
	return []string{
		"APP_USER_UI_PORT",
		"APP_PROVIDER_UI_PORT",
		"SV_UI_PORT",
		"SWAGGER_UI_PORT",
		"DB_PORT",
	}
}
