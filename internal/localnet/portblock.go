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

// UIPortEnvVarToStateKey maps each compose env-var name to the
// registry.State.Ports key under which the chosen host port is
// persisted. Used by ReuseOrAllocateUIPorts so an `up` after a
// `down` can hand the user back the same URLs they bookmarked.
//
// The mapping is the inverse of the assignments in RunUp step 8
// — keep them in sync. Zhe flagged in PR #20 #5/#7 that
// re-up should not surprise the user with new ports.
var UIPortEnvVarToStateKey = map[string]string{
	"APP_USER_UI_PORT":     "app_user_ui",
	"APP_PROVIDER_UI_PORT": "app_provider_ui",
	"SV_UI_PORT":           "sv_ui",
	"SWAGGER_UI_PORT":      "swagger_ui",
	"DB_PORT":              "postgres",
}

// ErrPortBusy is returned by ReuseOrAllocateUIPorts when a previously
// allocated host port is currently bound by another process and
// therefore cannot be re-used. The caller surfaces this as a user
// error (exit 2) — the right remediation is to free the port
// (`lsof -i :<port>`) or tear down the conflicting instance, not to
// silently fall back to a different port (that would defeat the
// stable-URL contract this helper exists to enforce).
var ErrPortBusy = fmt.Errorf("previously allocated host port is busy")

// ReuseOrAllocateUIPorts implements the stable-port contract from
// PR #20 #5/#7. Decision table:
//
//	prior state.Ports     | port still free | result
//	──────────────────────┼─────────────────┼─────────────────────────
//	nil / missing key / 0 |       —         | allocate ephemeral
//	non-zero              |      yes        | reuse the same port
//	non-zero              |      no         | ErrPortBusy (no fallback)
//
// The "no fallback on busy" rule is deliberate: silently switching
// ports would re-introduce exactly the bookmark-breakage the ticket
// is about. We surface a clear error and let the user decide.
//
// `prior` may be nil on first-run; that case is equivalent to "every
// key missing" and triggers full ephemeral allocation.
func ReuseOrAllocateUIPorts(envVars []string, prior map[string]int) (map[string]int, error) {
	out := make(map[string]int, len(envVars))
	for _, ev := range envVars {
		key, ok := UIPortEnvVarToStateKey[ev]
		if !ok {
			// Unknown env var — fall back to fresh allocation. (Test
			// fixtures may pass arbitrary names through
			// AllocateUIPorts; we preserve that contract.)
			p, err := allocateOneEphemeral()
			if err != nil {
				return nil, fmt.Errorf("allocate port for %s: %w", ev, err)
			}
			out[ev] = p
			continue
		}
		if prev, present := prior[key]; present && prev > 0 {
			if portIsFree(prev) {
				out[ev] = prev
				continue
			}
			return nil, fmt.Errorf("%w: %s previously bound %d", ErrPortBusy, ev, prev)
		}
		p, err := allocateOneEphemeral()
		if err != nil {
			return nil, fmt.Errorf("allocate port for %s: %w", ev, err)
		}
		out[ev] = p
	}
	return out, nil
}

// portIsFree returns true if we can briefly bind 127.0.0.1:port —
// proving no other process currently holds it. Same TOCTOU caveat as
// AllocateUIPorts: there's a millisecond window between our close()
// and docker compose's bind(); in practice this is reliable enough
// for dev tooling.
func portIsFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func allocateOneEphemeral() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	var port int
	if i := lastByte(addr, ':'); i >= 0 {
		if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err != nil {
			return 0, fmt.Errorf("parse allocated port %q: %w", addr, err)
		}
	}
	return port, nil
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
