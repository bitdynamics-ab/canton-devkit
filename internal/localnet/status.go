package localnet

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

const statusJWTRedaction = "<redacted>"

// StatusOptions captures `localnet status` flags.
type StatusOptions struct {
	Name       string
	Format     string // "table" (default) or "json"
	NoLive     bool
	IncludeJWT bool
}

// statusProberFn is the test seam for live Docker service status.
var statusProberFn func(ctx context.Context, state *registry.State) ([]types.ServiceStatus, error)

// RunStatus prints the health summary for one instance. Docker probe failures
// are soft-failures: users still get the registry view and an exit-0 status.
func RunStatus(ctx context.Context, out io.Writer, errw io.Writer, opts *StatusOptions) int {
	inst, err := CollectStatus(ctx, opts.Name, !opts.NoLive, opts.IncludeJWT)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			_, _ = fmt.Fprintf(errw, "no LocalNet instance named %q\nRun `dpm localnet list` to see available instances.\n", opts.Name)
			return ExitUserError
		}
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitRuntimeFailure
	}

	switch {
	case opts.NoLive:
		_, _ = fmt.Fprintln(errw, term.Warnc("warning: --no-live skips docker; service health and live ports may be stale"))
	case inst.LiveProbeFailed:
		_, _ = fmt.Fprintln(errw, term.Warnc("warning: docker compose ps failed; live service health unavailable, showing registry view only"))
	}

	switch opts.Format {
	case "", "table":
		writeStatusTable(out, inst)
	case "json":
		if err := writeStatusJSON(out, inst); err != nil {
			_, _ = fmt.Fprintf(errw, "%s\n", err)
			return ExitRuntimeFailure
		}
	default:
		_, _ = fmt.Fprintf(errw, "--format must be table or json (got %q)\n", opts.Format)
		return ExitUserError
	}
	return ExitSuccess
}

// CollectStatus is the exported entry point for non-CLI callers. includeJWT
// controls JWT redaction; unauthenticated surfaces must pass false.
func CollectStatus(ctx context.Context, name string, live, includeJWT bool) (types.Instance, error) {
	s, err := registry.Read(name)
	if err != nil {
		return types.Instance{}, err
	}

	inst := types.Instance{
		SchemaVersion:   types.SchemaVersion,
		Name:            s.Name,
		SpliceVersion:   s.SpliceVersion,
		Status:          string(s.Status),
		CreatedAt:       s.CreatedAt,
		Uptime:          uptimeSince(s.CreatedAt, s.Status),
		ComposeProject:  s.ComposeProject,
		DockerNetwork:   s.DockerNetwork,
		ContainerPrefix: s.ContainerPrefix,
		ProjectDir:      s.ProjectDir,
		DataDir:         s.DataDir,
		Endpoints:       endpointsFromPorts(s.Ports),
		Credentials:     credentialsFor(s.Credentials, includeJWT),
	}

	if live {
		prober := statusProberFn
		if prober == nil {
			prober = defaultStatusProber
		}
		svcs, perr := prober(ctx, s)
		if perr != nil {
			inst.LiveProbeFailed = true
			inst.Services = nil
		} else {
			inst.Services = svcs
			// Probe only when docker itself answered, so
			// "unreachable UI" always points at the instance, not
			// at a stopped daemon.
			if s.Status == registry.StatusRunning {
				probeUIEndpoints(ctx, s, inst.Endpoints)
			}
		}
	}

	return inst, nil
}

func defaultStatusProber(ctx context.Context, s *registry.State) ([]types.ServiceStatus, error) {
	runner := &docker.ComposeRunner{
		ProjectName: s.ComposeProject,
		WorkDir:     s.ProjectDir,
	}
	out, err := runner.Ps(ctx)
	if err != nil {
		return nil, err
	}
	var svcs []types.ServiceStatus
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		svcs = append(svcs, types.ServiceStatus{
			Name:  trimContainerPrefix(parts[0], s.ContainerPrefix),
			State: collapseState(parts[1], parts[2]),
			Image: parts[3],
			Ports: stringAt(parts, 4),
		})
	}
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
	return svcs, nil
}

func collapseState(state, health string) string {
	switch state {
	case "running":
		switch health {
		case "healthy", "":
			return "healthy"
		case "starting":
			return "syncing"
		case "unhealthy":
			return "unhealthy"
		default:
			return health
		}
	case "paused":
		return "paused"
	case "exited", "dead", "removing":
		return "exited"
	default:
		return state
	}
}

func endpointsFromPorts(ports map[string]int) []types.Endpoint {
	if len(ports) == 0 {
		return nil
	}
	type meta struct{ label, scheme string }
	known := map[string]meta{
		"app_user_ui":     {"Wallet · app-user", "http"},
		"app_provider_ui": {"Wallet · app-provider", "http"},
		// The bare sv_ui port serves the sv wallet; Scan UI sits
		// behind the scan.localhost vhost on the same port.
		"sv_ui":               {"Wallet · sv", "http"},
		"swagger_ui":          {"Swagger · JSON API", "http"},
		"postgres":            {"Postgres", "postgresql"},
		"app_user_ledger":     {"Ledger API · app-user", "grpc"},
		"app_provider_ledger": {"Ledger API · app-provider", "grpc"},
		"sv_ledger":           {"Ledger API · sv", "grpc"},
	}
	logicalNames := make([]string, 0, len(ports))
	for k := range ports {
		logicalNames = append(logicalNames, k)
	}
	sort.Strings(logicalNames)

	out := make([]types.Endpoint, 0, len(ports))
	for _, k := range logicalNames {
		p := ports[k]
		if p == 0 {
			continue
		}
		m, ok := known[k]
		if !ok {
			m = meta{label: k, scheme: "tcp"}
		}
		out = append(out, types.Endpoint{
			Key:    k,
			Label:  m.label,
			Port:   p,
			Scheme: m.scheme,
			URL:    fmt.Sprintf("%s://localhost:%d", m.scheme, p),
		})
	}
	return out
}

func credentialsFor(in map[string]registry.Credential, includeJWT bool) map[string]types.Credential {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]types.Credential, len(in))
	for k, v := range in {
		jwt := v.JWT
		if !includeJWT && jwt != "" {
			jwt = statusJWTRedaction
		}
		out[k] = types.Credential{Role: v.Role, User: v.User, Audience: v.Audience, JWT: jwt}
	}
	return out
}

func uptimeSince(createdAt string, status registry.Status) string {
	if status != registry.StatusRunning || createdAt == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		return ""
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func trimContainerPrefix(containerName, prefix string) string {
	if prefix == "" {
		return containerName
	}
	return strings.TrimPrefix(containerName, prefix)
}

func stringAt(parts []string, i int) string {
	if i >= len(parts) {
		return ""
	}
	return parts[i]
}

func writeStatusTable(w io.Writer, inst types.Instance) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, headerLine(inst))
	_, _ = fmt.Fprintln(w)

	if inst.Services == nil {
		right := "registry view only"
		body := term.Dimc("(live data skipped; run without --no-live to query docker)")
		if inst.LiveProbeFailed {
			right = "docker query failed — registry view follows"
			body = term.Dimc("(no live data; run `docker info` to diagnose)")
		}
		_, _ = fmt.Fprintln(w, term.Section("Services", right, body, 0))
	} else if len(inst.Services) == 0 {
		_, _ = fmt.Fprintln(w, term.Section("Services", "",
			term.Dimc("(no running containers for this project)"), 0))
	} else {
		rows := make([][]string, 0, len(inst.Services))
		for _, s := range inst.Services {
			rows = append(rows, []string{term.Bold(s.Name), stateGlyph(s.State), s.Image, s.Ports})
		}
		_, _ = fmt.Fprintln(w, term.Section("Services", "", term.Table(
			[]term.Column{{Label: "service"}, {Label: "state"}, {Label: "image"}, {Label: "ports"}}, rows), 0))
	}

	if len(inst.Endpoints) > 0 {
		var endpointBody strings.Builder
		for i, e := range inst.Endpoints {
			value := term.Brandc(e.URL)
			if e.Reachability == types.ReachabilityUnreachable {
				value += " " + term.Errorc(term.Glyphs.Cross+" unreachable")
			}
			endpointBody.WriteString(term.KV(e.Label, value, 28))
			if i < len(inst.Endpoints)-1 {
				endpointBody.WriteString("\n")
			}
		}
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, term.Section("Endpoints", "", endpointBody.String(), 0))
	}

	if len(inst.Credentials) > 0 {
		var idBody strings.Builder
		roles := make([]string, 0, len(inst.Credentials))
		for r := range inst.Credentials {
			roles = append(roles, r)
		}
		sort.Strings(roles)
		for i, r := range roles {
			cred := inst.Credentials[r]
			idBody.WriteString(term.KV(r, fmt.Sprintf("%s %s", term.Textc(cred.User), term.Dimc("· "+cred.Audience)), 16))
			if i < len(roles)-1 {
				idBody.WriteString("\n")
			}
		}
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, term.Section("Identities", "", idBody.String(), 0))
	}

	writeUIReachabilityWarning(w, inst)
}

// writeUIReachabilityWarning renders the remediation box for UI
// endpoints that never answered HTTP. Placed last so the remediation
// is the final thing the eye lands on, matching doctor's
// table-then-box layout.
func writeUIReachabilityWarning(w io.Writer, inst types.Instance) {
	var broken []types.Endpoint
	for _, e := range inst.Endpoints {
		if e.Reachability == types.ReachabilityUnreachable {
			broken = append(broken, e)
		}
	}
	if len(broken) == 0 {
		return
	}
	var body strings.Builder
	body.WriteString(term.Textc(fmt.Sprintf("%d UI endpoint%s not serving HTTP:",
		len(broken), preflightPluralS(len(broken)))))
	for _, e := range broken {
		body.WriteString("\n  ")
		body.WriteString(term.Errorc(term.Glyphs.Cross))
		body.WriteString(" ")
		body.WriteString(term.Textc(e.Label))
		if e.ReachabilityDetail != "" {
			body.WriteString(term.Dimc(" — " + e.ReachabilityDetail))
		}
	}
	body.WriteString("\n\n")
	body.WriteString(term.Dimc("Usually a stale port overlay generated by an older DevKit.\n"))
	body.WriteString(term.Textc(fmt.Sprintf(
		"Re-run `dpm localnet up --name %s` (or Recreate in the Web UI) to regenerate the instance's overlays.",
		inst.Name)))
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, term.Box(term.BoxWarn, body.String()))
}

func headerLine(inst types.Instance) string {
	var b strings.Builder
	b.WriteString(term.Dimc("Name      "))
	b.WriteString(term.Bold(inst.Name))
	b.WriteString("   ")
	b.WriteString(term.Dimc("Splice "))
	b.WriteString(term.Brandc(inst.SpliceVersion))
	if inst.Uptime != "" {
		b.WriteString("   ")
		b.WriteString(term.Dimc("Uptime "))
		b.WriteString(term.Textc(inst.Uptime))
	}
	b.WriteString("   ")
	b.WriteString(term.Dimc("State "))
	b.WriteString(stateGlyph(inst.Status))
	return b.String()
}

func stateGlyph(state string) string {
	switch state {
	case "healthy", string(registry.StatusRunning):
		return term.Successc("● healthy")
	case string(registry.StatusCreating):
		return term.Brandc("◐ creating")
	case "syncing":
		return term.Warnc("◐ syncing")
	case "unhealthy":
		return term.Errorc("◯ unhealthy")
	case "paused":
		return term.Warnc("◐ paused")
	case "exited", string(registry.StatusFailed):
		return term.Errorc("⊗ exited")
	case "disabled", string(registry.StatusStopped):
		return term.Dimc("○ stopped")
	case string(registry.StatusPartial):
		return term.Warnc("◐ partial")
	default:
		return term.Dimc(state)
	}
}

func writeStatusJSON(w io.Writer, inst types.Instance) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(inst)
}
