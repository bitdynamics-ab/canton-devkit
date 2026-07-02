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

// Records the participant gRPC + JSON API ports the Canton container
// exposes, so consumers can dial them without a manual
// `--admin-host=localhost:<port>` flag.
//
// The Canton container in Splice LocalNet exposes 9 ports — 3 APIs
// (Ledger gRPC, Admin gRPC, JSON Ledger HTTP) × 3 party roles
// (app_user, app_provider, sv), all assigned dynamically by Docker at
// compose-up time, so they must be captured after the services come up.
//
// Key naming convention: `participant_<api>_<role>`. Once shipped,
// never renamed — frontends and CLI consumers branch on these literal
// keys.

// CantonPortInternal lists the canonical internal ports of the Canton
// container's participant APIs, keyed by the persistence key used in
// state.json's Ports map. Role keys use hyphens to match the
// state.Credentials keys ("app-user", "app-provider", "sv") and the
// CLI's --role flag — the same string indexes both the credential and
// the per-role port. (Other registry keys like "app_user_ui" use
// underscores; the inconsistency is inherited.)
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
// query: 2 s × 9 ports = 18 s worst case, well under the surrounding
// job timeout but generous enough to ride out a busy daemon.
const captureCantonPortsTimeout = 2 * time.Second

// composePortCmd is the test seam — production routes through
// exec.CommandContext; tests inject a deterministic stub.
var composePortCmd = func(ctx context.Context, project, service string, internal int) ([]byte, error) {
	cmd := exec.CommandContext(ctx,
		"docker", "compose", "-p", project, "port", service, strconv.Itoa(internal))
	return cmd.Output()
}

// CaptureCantonPorts runs `docker compose -p <project> port canton <internal>`
// for each canonical Canton port and returns a map suitable for merging
// into state.Ports. Best-effort: on any failure (container not started,
// daemon hiccup, port not published in this Splice version) the key is
// omitted rather than written as 0 — "key missing" means "unknown", not
// "the API isn't there". Persisting 0 would confuse later consumers,
// and failing the bring-up over a port-capture glitch would be worse.
func CaptureCantonPorts(ctx context.Context, project string) map[string]int {
	out := make(map[string]int, len(CantonPortInternal))
	for key, internal := range CantonPortInternal {
		subCtx, cancel := context.WithTimeout(ctx, captureCantonPortsTimeout)
		raw, err := composePortCmd(subCtx, project, "canton", internal)
		cancel()
		if err != nil {
			continue
		}
		host, err := parseComposePortLine(string(raw))
		if err != nil {
			// compose port can emit an empty line when the service has
			// no published port here — treat as unknown, like an error.
			continue
		}
		out[key] = host
	}
	return out
}

// CaptureMetricsPorts discovers the loopback host ports for the canton
// and splice :10013 Prometheus-reporter endpoints, keyed as
// PortCantonMetrics / PortSpliceMetrics. The shared host-level Prometheus
// scrapes these via host.docker.internal:<port>. An absent key
// means the instance didn't publish :10013 (e.g. an older bring-up that
// predates the publish) — the caller registers whatever was found.
func CaptureMetricsPorts(ctx context.Context, project string) map[string]int {
	out := make(map[string]int, 2)
	for _, svc := range []struct{ service, key string }{
		{"canton", PortCantonMetrics},
		{"splice", PortSpliceMetrics},
	} {
		subCtx, cancel := context.WithTimeout(ctx, captureCantonPortsTimeout)
		raw, err := composePortCmd(subCtx, project, svc.service, 10013)
		cancel()
		if err != nil {
			continue
		}
		host, err := parseComposePortLine(string(raw))
		if err != nil {
			continue
		}
		out[svc.key] = host
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
