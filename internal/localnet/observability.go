package localnet

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// This file holds the neutral observability-toggle orchestration that
// BOTH the Web UI handler (POST /api/instances/{name}/observability)
// and the CLI verb (`dpm localnet observability enable|disable`) call.
// The logic is pure orchestration (materialize overlay → docker
// compose up/stop+rm → discover host port), so it lives in the neutral
// package; the two surfaces are thin wrappers that resolve the
// instance, hold the per-instance lock, and translate the typed result
// into an HTTP envelope / CLI text respectively.

// ObservabilityState reports which observability sidecars are currently
// running for an instance, derived from the persisted host-port map: a
// recorded prometheus_ui / grafana_ui port means the corresponding
// sidecar is up. This is the same signal the metrics scrape path uses.
type ObservabilityState struct {
	Prometheus     bool
	Grafana        bool
	PrometheusPort int
	GrafanaPort    int
}

// ReadObservabilityState derives the current per-component state from
// the instance's persisted port map. Nil-safe.
func ReadObservabilityState(state *registry.State) ObservabilityState {
	var s ObservabilityState
	if state == nil {
		return s
	}
	if p, ok := state.Ports["prometheus_ui"]; ok && p > 0 {
		s.Prometheus = true
		s.PrometheusPort = p
	}
	if p, ok := state.Ports["grafana_ui"]; ok && p > 0 {
		s.Grafana = true
		s.GrafanaPort = p
	}
	return s
}

// ObservabilityResult is the typed outcome of SetObservability — the
// resolved target state plus the discovered host ports and any
// non-fatal advisory (e.g. Grafana enabled without Prometheus). Both
// surfaces project this into their own response shape.
type ObservabilityResult struct {
	Prometheus     bool
	Grafana        bool
	PrometheusPort int // 0 when Prometheus is off
	GrafanaPort    int // 0 when Grafana is off
	Warning        string
}

// GrafanaWithoutPrometheusWarning is the advisory surfaced (not an
// error) when Grafana is enabled without Prometheus — a user may
// legitimately point Grafana at an external scrape source, but an
// empty dashboard is a common surprise. Shared so the CLI and UI emit
// the same wording.
const GrafanaWithoutPrometheusWarning = "Grafana enabled without Prometheus — dashboards " +
	"will have no bundled data source. Enable Prometheus or " +
	"configure an external scrape source manually."

// SetObservability brings the prometheus / grafana sidecars on a
// RUNNING instance to the requested (wantProm, wantGraf) target without
// disturbing canton/splice, then persists the resulting host ports into
// state.json and returns the typed result.
//
// Contract:
//   - The CALLER must hold the per-instance registry lock and must have
//     already verified the instance is running. SetObservability re-reads
//     state under the lock before persisting so it never clobbers a
//     concurrent writer.
//   - Only the components whose desired state DIFFERS from the current
//     state are touched: enabling an already-on sidecar is a no-op, and
//     the still-running neighbor's port is preserved (Docker reuses the
//     container when the env values are unchanged).
//   - logw (may be nil) receives the overlay's "preserving local edits"
//     drift notices.
//
// On a docker failure it returns the combined command output wrapped in
// the error so the caller can log it; partial progress already applied
// to the live stack is left in place (the caller surfaces a 502 / exit
// code and the user can re-issue the toggle).
func SetObservability(ctx context.Context, state *registry.State, wantProm, wantGraf bool, logw io.Writer) (ObservabilityResult, error) {
	if state == nil {
		return ObservabilityResult{}, fmt.Errorf("SetObservability: nil state")
	}

	cur := ReadObservabilityState(state)

	res := ObservabilityResult{Prometheus: wantProm, Grafana: wantGraf}
	if wantGraf && !wantProm {
		res.Warning = GrafanaWithoutPrometheusWarning
	}

	var promPort int
	if wantProm && !cur.Prometheus {
		out, port, err := enableObservabilitySidecar(ctx, state, PrometheusProfileName, "prometheus", 9090, logw)
		if err != nil {
			return res, fmt.Errorf("enable prometheus: %w\noutput:\n%s", err, out)
		}
		promPort = port
	} else if !wantProm && cur.Prometheus {
		if out, err := disableObservabilitySidecar(ctx, state, "prometheus"); err != nil {
			return res, fmt.Errorf("disable prometheus: %w\noutput:\n%s", err, out)
		}
	}

	var grafPort int
	if wantGraf && !cur.Grafana {
		out, port, err := enableObservabilitySidecar(ctx, state, GrafanaProfileName, "grafana", 3000, logw)
		if err != nil {
			return res, fmt.Errorf("enable grafana: %w\noutput:\n%s", err, out)
		}
		grafPort = port
	} else if !wantGraf && cur.Grafana {
		if out, err := disableObservabilitySidecar(ctx, state, "grafana"); err != nil {
			return res, fmt.Errorf("disable grafana: %w\noutput:\n%s", err, out)
		}
	}

	// Re-read under the caller's lock before persisting so we don't
	// clobber a concurrent writer that ran AND released between the
	// caller's Read and now (defensive but cheap — the lock makes this
	// rare). Fall back to the in-memory state when the re-read fails so
	// a transient registry hiccup doesn't strand a just-toggled stack.
	if fresh, err := registry.Read(state.Name); err == nil {
		state = fresh
	}
	if state.Ports == nil {
		state.Ports = map[string]int{}
	}

	if wantProm {
		if promPort != 0 {
			state.Ports["prometheus_ui"] = promPort
		}
	} else {
		delete(state.Ports, "prometheus_ui")
	}
	if wantGraf {
		if grafPort != 0 {
			state.Ports["grafana_ui"] = grafPort
		}
	} else {
		delete(state.Ports, "grafana_ui")
	}

	// Keep the persisted Profiles in sync so a later down → up re-up
	// re-enables exactly the components that are on NOW. We map
	// the two booleans to the per-component profile names.
	state.Profiles = syncObservabilityProfiles(state.Profiles, wantProm, wantGraf)

	if err := registry.Write(state); err != nil {
		return res, fmt.Errorf("persist observability toggle: %w", err)
	}

	res.PrometheusPort = state.Ports["prometheus_ui"]
	res.GrafanaPort = state.Ports["grafana_ui"]
	return res, nil
}

// syncObservabilityProfiles updates the persisted profile list to
// reflect the post-toggle observability state, preserving any
// non-observability profiles (e.g. tokens-v2) untouched. It collapses
// the legacy umbrella `observability` into its per-component members so
// the stored set is unambiguous after a partial toggle (e.g. disabling
// just Grafana on an instance created with `--profile observability`
// must leave `prometheus` recorded, not the umbrella).
func syncObservabilityProfiles(existing []string, prom, graf bool) []string {
	out := make([]string, 0, len(existing)+2)
	for _, p := range existing {
		switch p {
		case ObservabilityProfileName, PrometheusProfileName, GrafanaProfileName:
			// drop — re-added below from the resolved booleans
		default:
			out = append(out, p)
		}
	}
	if prom {
		out = append(out, PrometheusProfileName)
	}
	if graf {
		out = append(out, GrafanaProfileName)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// enableObservabilitySidecar runs the docker-compose subcommands that
// materialize the observability overlay and bring up a single named
// sidecar service under the matching per-component profile. Returns the
// captured combined output (for the error path) and the discovered host
// port. portInternal is the in-container port to look up via
// `docker compose port <svc> <port>` after the up succeeds.
func enableObservabilitySidecar(ctx context.Context, state *registry.State, profile, service string, portInternal int, logw io.Writer) (string, int, error) {
	// Capture the overlay's "preserving local edits" drift notices so a
	// caller toggling a sidecar still learns their local dashboard /
	// scrape-config copy diverges from the bundled default.
	var overlayWarn bytes.Buffer
	overlay, err := MaterializeObservabilityOverlay(state.DataDir, state.ProjectDir, &overlayWarn)
	if err != nil {
		return "", 0, fmt.Errorf("materialize overlay: %w", err)
	}
	if overlayWarn.Len() > 0 && logw != nil {
		_, _ = fmt.Fprintf(logw, "observability overlay for %q: %s\n",
			state.Name, strings.TrimSpace(overlayWarn.String()))
	}
	hasOverlay := false
	for _, f := range state.ComposeFiles {
		if f == overlay {
			hasOverlay = true
			break
		}
	}
	if !hasOverlay {
		state.ComposeFiles = append(state.ComposeFiles, overlay)
	}

	// Reuse the instance's already-published UI host ports; let docker
	// auto-assign the observability ports (HOST_PORT=0) — the freshly
	// allocated port is discovered below via `docker compose port`.
	uiOverrides := map[string]int{
		"APP_USER_UI_PORT":     state.Ports["app_user_ui"],
		"APP_PROVIDER_UI_PORT": state.Ports["app_provider_ui"],
		"SV_UI_PORT":           state.Ports["sv_ui"],
		"SWAGGER_UI_PORT":      state.Ports["swagger_ui"],
		"DB_PORT":              state.Ports["postgres"],
		"PROMETHEUS_HOST_PORT": existingOrZeroPort(state.Ports, "prometheus_ui"),
		"GRAFANA_HOST_PORT":    existingOrZeroPort(state.Ports, "grafana_ui"),
	}
	// Force a fresh allocation for the service we're enabling.
	switch service {
	case "prometheus":
		uiOverrides["PROMETHEUS_HOST_PORT"] = 0
	case "grafana":
		uiOverrides["GRAFANA_HOST_PORT"] = 0
	}
	cenv, err := ComposeEnvForInstance(state, uiOverrides)
	if err != nil {
		return "", 0, fmt.Errorf("rebuild compose env: %w", err)
	}

	args := []string{"compose", "-p", state.ComposeProject}
	for _, f := range cenv.EnvFiles {
		args = append(args, "--env-file", f)
	}
	for _, f := range state.ComposeFiles {
		args = append(args, "-f", f)
	}
	args = append(args, "--profile", profile, "up", "-d", service)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = state.ProjectDir
	cmd.Env = cenv.Env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), 0, fmt.Errorf("docker compose up: %w", err)
	}

	portCmd := exec.CommandContext(ctx, "docker", "compose",
		"-p", state.ComposeProject, "port", service, strconv.Itoa(portInternal))
	portCmd.Dir = state.ProjectDir
	rawPort, perr := portCmd.CombinedOutput()
	if perr != nil {
		return string(out) + "\n" + string(rawPort), 0,
			fmt.Errorf("discover %s host port: %w", service, perr)
	}
	port := parseObservabilityHostPort(string(rawPort))
	if port == 0 {
		return string(out) + "\n" + string(rawPort), 0,
			fmt.Errorf("could not parse %s host port from %q", service, string(rawPort))
	}
	return string(out), port, nil
}

// disableObservabilitySidecar stops + removes a single named sidecar
// (prometheus or grafana) without touching anything else. We
// deliberately do NOT mutate state.ComposeFiles — keeping the overlay
// in the list makes a future re-enable a no-op materialize + spin-up.
func disableObservabilitySidecar(ctx context.Context, state *registry.State, service string) (string, error) {
	stopCmd := exec.CommandContext(ctx, "docker", "compose",
		"-p", state.ComposeProject, "stop", service)
	stopCmd.Dir = state.ProjectDir
	if out, err := stopCmd.CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("docker compose stop %s: %w", service, err)
	}
	rmCmd := exec.CommandContext(ctx, "docker", "compose",
		"-p", state.ComposeProject, "rm", "-f", service)
	rmCmd.Dir = state.ProjectDir
	if out, err := rmCmd.CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("docker compose rm %s: %w", service, err)
	}
	return "", nil
}

// existingOrZeroPort returns the int at key or 0 if absent. Keeps the
// still-running sidecar's port stable when toggling its neighbor.
func existingOrZeroPort(m map[string]int, key string) int {
	if v, ok := m[key]; ok {
		return v
	}
	return 0
}

// parseObservabilityHostPort pulls the port number out of `docker
// compose port` output. Output shape examples:
//
//	0.0.0.0:60471
//	127.0.0.1:60471
//	[::]:60471
//
// Returns 0 if no port found.
func parseObservabilityHostPort(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	idx := strings.LastIndex(s, ":")
	if idx < 0 || idx == len(s)-1 {
		return 0
	}
	p, err := strconv.Atoi(strings.TrimSpace(s[idx+1:]))
	if err != nil {
		return 0
	}
	return p
}
