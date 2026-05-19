package localnet

import (
	"fmt"
	"net"
)

// AllocateUIPorts picks a free OS-assigned host port for each envVar
// entry. We always allocate ephemerally — DevKit manages host ports so
// users don't have to care which port any service is bound to. The
// resulting map is exported as compose env (APP_USER_UI_PORT=… etc.) and
// also persisted to the registry so `localnet status` / `creds` /
// `logs` can recover the bindings.
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
