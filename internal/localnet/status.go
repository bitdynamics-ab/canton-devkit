package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// StatusOptions captures `localnet status` flags.
type StatusOptions struct {
	Name   string
	Format string // "text" (default) or "json"
}

// ValidateFormat is defined in format.go (single source of truth shared
// with internal/dar/*).

// composeServiceStatus is the per-service summary we present in `status`.
type composeServiceStatus struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
	Image   string `json:"image,omitempty"`
	Service string `json:"service,omitempty"`
}

// RunStatus prints the health summary for one instance. Exit code:
//   - 0 if every service is running & healthy (or has no healthcheck)
//   - 2 if any service is unhealthy or stopped
//   - 3 if the instance is unknown (separate from up's exit-code scheme)
func RunStatus(ctx context.Context, out io.Writer, errw io.Writer, opts *StatusOptions) int {
	state, err := registry.Read(opts.Name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(errw, "No instance named %q is registered.\n", opts.Name)
		return 3
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read state: %s\n", err)
		return ExitRuntimeFailure
	}

	services, queryErr := queryComposeServices(ctx, state.ComposeProject)
	allHealthy := len(services) > 0
	for _, s := range services {
		if s.State != "running" {
			allHealthy = false
		}
		if s.Health != "" && s.Health != "healthy" {
			allHealthy = false
		}
	}

	if opts.Format == "json" {
		writeStatusJSON(out, state, services, queryErr)
	} else {
		writeStatusText(out, state, services, queryErr)
	}

	if !allHealthy {
		return ExitPreflightFail
	}
	return ExitSuccess
}

// queryComposeServices runs `docker compose -p <project> ps --format json`
// and parses the per-container records. Docker compose v2 emits one JSON
// object per line.
func queryComposeServices(ctx context.Context, project string) ([]composeServiceStatus, error) {
	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", project, "ps", "--all", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	var services []composeServiceStatus
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw struct {
			Name    string
			State   string
			Health  string
			Image   string
			Service string
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		services = append(services, composeServiceStatus{
			Name:    raw.Name,
			State:   raw.State,
			Health:  raw.Health,
			Image:   raw.Image,
			Service: raw.Service,
		})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services, nil
}

func writeStatusText(out io.Writer, s *registry.State, services []composeServiceStatus, queryErr error) {
	_, _ = fmt.Fprintf(out, "Instance:        %s\n", s.Name)
	_, _ = fmt.Fprintf(out, "Splice version:  %s\n", s.SpliceVersion)
	_, _ = fmt.Fprintf(out, "Created:         %s\n", s.CreatedAt)
	_, _ = fmt.Fprintf(out, "Status (state):  %s\n", s.Status)
	_, _ = fmt.Fprintf(out, "Compose project: %s\n", s.ComposeProject)
	_, _ = fmt.Fprintln(out)

	if queryErr != nil {
		_, _ = fmt.Fprintf(out, "Could not query Docker: %s\n", queryErr)
		return
	}
	if len(services) == 0 {
		_, _ = fmt.Fprintln(out, "No containers found for this project. Already stopped?")
		return
	}
	_, _ = fmt.Fprintln(out, "Services:")
	_, _ = fmt.Fprintf(out, "  %-32s %-10s %-10s\n", "NAME", "STATE", "HEALTH")
	for _, svc := range services {
		health := svc.Health
		if health == "" {
			health = "-"
		}
		_, _ = fmt.Fprintf(out, "  %-32s %-10s %-10s\n", svc.Name, svc.State, health)
	}

	if len(s.Ports) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Endpoints:")
		for _, e := range orderedEndpointKeys() {
			if port, ok := s.Ports[e.key]; ok {
				_, _ = fmt.Fprintf(out, "  %-20s %s://localhost:%d\n", e.label+":", e.scheme, port)
			}
		}
	}
}

type statusJSON struct {
	State    *registry.State        `json:"state"`
	Services []composeServiceStatus `json:"services"`
	Error    string                 `json:"error,omitempty"`
}

func writeStatusJSON(out io.Writer, s *registry.State, services []composeServiceStatus, queryErr error) {
	payload := statusJSON{State: s, Services: services}
	if queryErr != nil {
		payload.Error = queryErr.Error()
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}
