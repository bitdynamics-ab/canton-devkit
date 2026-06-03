package localnet

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// BIT-190 — record the participant gRPC + JSON API ports the Canton
// container exposes, so the Web UI can talk to them without a manual
// `--admin-host=localhost:<port>` flag (the CLI's only escape hatch
// today; see internal/cli/localnet/dar/connect.go ~L88 for the
// historical "we don't record this" comment).
//
// The Canton container in Splice LocalNet exposes 9 ports — 3 APIs
// (Ledger gRPC, Admin gRPC, JSON Ledger HTTP) × 3 party roles
// (app_user, app_provider, sv). All assigned dynamically by Docker
// at compose-up time, so they MUST be captured after the services
// come up — we can't predict them.
//
// Naming convention pinned by the proposal: `participant_<api>_<role>`.
// Once shipped, never renamed — frontends and CLI consumers branch on
// these literal keys.
//
// # Internal port layout (constant per Canton release)
//
// app_user:      2901 (ledger) · 2902 (admin) · 2975 (json)
// app_provider:  3901 (ledger) · 3902 (admin) · 3975 (json)
// sv:            4901 (ledger) · 4902 (admin) · 4975 (json)
//
// The "<role>9XX" pattern is from Splice's compose env (see the cached
// compose.yaml's `canton:ports:` block).
//
// # Behaviour
//
// CaptureCantonPorts is BEST-EFFORT. If `docker compose port` fails for
// any reason (container not yet started, docker daemon hiccup, port not
// exposed in this Splice version), the corresponding key is OMITTED
// from the returned map rather than written as 0. Callers should treat
// "key missing" as "I don't know" — not "the API isn't there".
//
// This avoids two failure modes:
//   1. Bricking an otherwise-healthy `up` because of a port-capture
//      glitch (the user can still use the UI ports).
//   2. Persisting 0 as a real port and confusing a later consumer
//      that doesn't know to treat 0 as "unset".

// CantonPortInternal lists the canonical internal ports of the Canton
// container's participant APIs, keyed by the persistence key we use
// in state.json's Ports map.
// Role keys use HYPHENS to match the existing state.Credentials
// keys ("app-user", "app-provider", "sv") and the CLI's --role
// flag default — so the same string indexes both the credential
// AND the per-role port in state.json.
//
// Note: other registry keys like "app_user_ui" use underscores;
// that predates the credentials work and is the inconsistency we
// inherited. Aligning all keys to one style is tracked separately.
var CantonPortInternal = map[string]int{
	// Ledger gRPC API per role
	"participant_ledger_app-user":     2901,
	"participant_ledger_app-provider": 3901,
	"participant_ledger_sv":           4901,

	// Admin gRPC API per role
	"participant_admin_app-user":     2902,
	"participant_admin_app-provider": 3902,
	"participant_admin_sv":           4902,

	// JSON Ledger HTTP API per role
	"participant_json_app-user":     2975,
	"participant_json_app-provider": 3975,
	"participant_json_sv":           4975,
}

// captureCantonPortsTimeout caps the docker subprocess time per port
// query. 2 s × 9 ports = 18 s worst case before we give up entirely —
// well under the surrounding job timeout but generous enough to ride
// out a transiently-busy daemon.
const captureCantonPortsTimeout = 2 * time.Second

// composePortCmd is the test seam — production routes through
// exec.CommandContext; tests inject a deterministic stub.
var composePortCmd = func(ctx context.Context, project, service string, internal int) ([]byte, error) {
	cmd := exec.CommandContext(ctx,
		"docker", "compose", "-p", project, "port", service, strconv.Itoa(internal))
	return cmd.Output()
}

// CaptureCantonPorts runs `docker compose -p <project> port canton <internal>`
// for each canonical Canton port and returns a map suitable for
// merging into state.Ports. Keys missing from the return value indicate
// that port wasn't reachable — see the package docs for why we don't
// represent that as 0.
func CaptureCantonPorts(ctx context.Context, project string) map[string]int {
	out := make(map[string]int, len(CantonPortInternal))
	for key, internal := range CantonPortInternal {
		subCtx, cancel := context.WithTimeout(ctx, captureCantonPortsTimeout)
		raw, err := composePortCmd(subCtx, project, "canton", internal)
		cancel()
		if err != nil {
			// Most likely cause: the container exists but doesn't
			// publish this port (e.g. observability-only ports on
			// non-observability instances, or a Splice version that
			// dropped a role). Skip silently — the absence of the
			// key is the signal.
			continue
		}
		host, err := parseComposePortLine(string(raw))
		if err != nil {
			// docker compose port can return an empty line when the
			// service has no published port for that internal port.
			// Treat as "unknown" — same as the error case.
			continue
		}
		out[key] = host
	}
	return out
}

// parseComposePortLine extracts the host port from a `docker compose
// port` output line. The CLI emits one of:
//
//	"0.0.0.0:53020\n"
//	"[::]:53020\n"
//	"127.0.0.1:53020\n"
//
// or — depending on Docker / compose version and dual-stack listening —
// MULTIPLE lines (IPv4 + IPv6). We take the first non-empty line, ignore
// the host (we always serve loopback locally), and parse the port.
func parseComposePortLine(out string) (int, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// SplitHostPort handles both "host:port" and "[::]:port" cleanly.
		_, portStr, err := net.SplitHostPort(line)
		if err != nil {
			continue
		}
		p, err := strconv.Atoi(portStr)
		if err != nil || p <= 0 || p > 65535 {
			continue
		}
		return p, nil
	}
	return 0, fmt.Errorf("no valid host:port line in compose port output: %q", out)
}
