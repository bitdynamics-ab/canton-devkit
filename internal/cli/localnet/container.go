package localnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildContainer wires `dpm localnet container <verb>` — the CLI
// mirror of the per-container HTTP endpoints under
// /api/instances/{name}/containers/. Per AGENTS.md "CLI ↔ Web UI
// parity": every per-container operation surfaced in the Web UI
// must also have a CLI verb here, calling into the same shared
// internal/localnet/containers package.
//
// Sub-verbs:
//
//	list     → list containers + state/health, mirror of
//	           GET /api/instances/{name}/containers
//	restart  → restart one container, mirror of
//	           POST /api/instances/{name}/containers/{c}/restart
//	logs     → tail one container's logs, mirror of
//	           GET /api/instances/{name}/containers/{c}/logs
//
// Future `start`, `stop`, `pause`, `unpause` slot in naturally
// under the same parent without polluting the top-level localnet
// command space.
func buildContainer() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container",
		Short:         "Operate on individual containers within an instance",
		Long:          "List, restart, or tail logs for containers in a LocalNet instance.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(buildContainerList())
	cmd.AddCommand(buildContainerRestart())
	cmd.AddCommand(buildContainerLogs())
	return cmd
}

// validServiceArg loosely validates the second positional arg
// (container name) before it reaches the shared library — the
// library re-validates via BelongsToProject, but a friendlier
// up-front error avoids a docker subprocess for typos. Same
// regex the HTTP handler uses (validContainerName).
var validServiceArg = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,127}$`)

func resolveInstance(name string) (*registry.State, error) {
	if err := registry.ValidateName(name); err != nil {
		return nil, fmt.Errorf("invalid instance name: %w", err)
	}
	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return nil, fmt.Errorf("instance %q is not registered; run `dpm localnet list` to see known names", name)
		}
		return nil, fmt.Errorf("read state for %q: %w", name, err)
	}
	return state, nil
}

func buildContainerList() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:           "list <instance>",
		Aliases:       []string{"ls", "ps"},
		Short:         "List containers for an instance with state + health",
		Long:          "Show each container's state, healthcheck result, and docker status line.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localnet_validateFormat(format, "text", "json"); err != nil {
				return err
			}
			state, err := resolveInstance(args[0])
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			infos, err := containers.List(ctx, state.ComposeProject)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"docker probe failed: "+err.Error())
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"instance":   args[0],
					"project":    state.ComposeProject,
					"containers": infos,
				})
			}
			if len(infos) == 0 {
				_, _ = fmt.Fprintln(out, term.Dimc(
					"no containers — the compose project may have been torn down"))
				return nil
			}
			renderContainerSection(out, args[0], state.ComposeProject, infos)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

// renderContainerSection draws the table inside a Section header
// (matches the JSX ScreenStatus / Container Health mockup). Each
// row is prefixed with a coloured glyph reflecting health/state
// — same convention as the Web UI's signalFor() function so a
// user moving between surfaces reads the same signal at a glance.
func renderContainerSection(out cobraWriter, instance, project string, infos []containers.Info) {
	var body strings.Builder
	for _, c := range infos {
		body.WriteString(renderContainerRow(c))
		body.WriteString("\n")
	}
	right := fmt.Sprintf("project %s · %d containers", project, len(infos))
	_, _ = fmt.Fprintln(out, term.Section(
		"containers · "+instance,
		right,
		strings.TrimRight(body.String(), "\n"),
		0,
	))
}

// renderContainerRow formats one container line with a status
// glyph + service name + state/health + raw status. Service names
// are pinned to a 28-rune column so the rest aligns; container
// names in Splice's compose are well under that.
func renderContainerRow(c containers.Info) string {
	glyph := containerGlyph(c)
	service := c.Service
	if len(service) > 28 {
		service = service[:25] + "..."
	}
	state := c.State
	if c.Health != "" {
		state = c.State + " · " + c.Health
	}
	return fmt.Sprintf("%s  %-28s  %-22s  %s",
		glyph, service, state, term.Dimc(c.Status))
}

// containerGlyph mirrors the Web UI's signalFor() — same colour
// vocabulary so the same incident reads identically in CLI and UI.
// See frontend/src/screens/ContainerHealth.tsx for the source.
func containerGlyph(c containers.Info) string {
	switch c.State {
	case "restarting":
		return term.Warnc("↻")
	case "dead", "exited":
		return term.Errorc("✕")
	case "paused":
		return term.Dimc("⏸")
	}
	switch c.Health {
	case "unhealthy":
		return term.Errorc("⊗")
	case "starting":
		return term.Brandc("●")
	case "healthy":
		return term.Successc("✓")
	}
	if c.State == "running" {
		return term.Successc("●")
	}
	return term.Dimc("·")
}

func buildContainerRestart() *cobra.Command {
	return &cobra.Command{
		Use:           "restart <instance> <service>",
		Short:         "Restart a single container within an instance",
		Long:          "Run docker restart on one container. Other containers stay up. The container must belong to the instance's compose project.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveInstance(args[0])
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			serviceOrContainer := args[1]
			if !validServiceArg.MatchString(serviceOrContainer) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"invalid container/service name (alphanumeric + . _ - only)")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			// Resolve service name → actual container name (the
			// CLI accepts either; the Web UI always passes the
			// full container name). Most users will type
			// `splice` not `pr432-splice`.
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			containerName, err := resolveContainerName(ctx, state.ComposeProject, serviceOrContainer)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			restartCtx, cancelR := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancelR()
			if err := containers.Restart(restartCtx, containerName); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Step(term.StepCheck,
				"restarted "+containerName, "", ""))
			return nil
		},
	}
}

func buildContainerLogs() *cobra.Command {
	var (
		tail  int
		since string
	)
	cmd := &cobra.Command{
		Use:           "logs <instance> <service>",
		Short:         "Tail logs for one container within an instance",
		Long:          "Returns the last N lines of one container's combined stdout+stderr. Accepts a short service name (e.g. `splice`) or the full container name (e.g. `pr432-splice`).",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveInstance(args[0])
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			serviceOrContainer := args[1]
			if !validServiceArg.MatchString(serviceOrContainer) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"invalid container/service name (alphanumeric + . _ - only)")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			containerName, err := resolveContainerName(ctx, state.ComposeProject, serviceOrContainer)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			logsCtx, cancelL := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancelL()
			body, err := containers.Logs(logsCtx, containerName, containers.LogsOptions{
				Tail:  tail,
				Since: since,
			})
			if body != "" {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), body)
				if len(body) > 0 && body[len(body)-1] != '\n' {
					_, _ = fmt.Fprintln(cmd.OutOrStdout())
				}
			}
			if err != nil && body == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 200, "Number of lines to show from the end (10-2000)")
	cmd.Flags().StringVar(&since, "since", "", "Show logs since duration, e.g. 5m, 30s, 2h")
	return cmd
}

// resolveContainerName accepts either a service short-name
// (e.g. `splice`) or the full container name (e.g.
// `pr432-splice`) and returns the canonical container name docker
// recognizes. Verifies the container actually belongs to the
// instance's compose project — defence-in-depth so a typo or
// hostile arg can't restart an arbitrary container on the host.
func resolveContainerName(ctx context.Context, project, serviceOrContainer string) (string, error) {
	infos, err := containers.List(ctx, project)
	if err != nil {
		return "", fmt.Errorf("docker probe failed: %w", err)
	}
	for _, c := range infos {
		if c.Name == serviceOrContainer || c.Service == serviceOrContainer {
			return c.Name, nil
		}
	}
	known := make([]string, 0, len(infos))
	for _, c := range infos {
		known = append(known, c.Service)
	}
	return "", fmt.Errorf("no container %q in project %q (known services: %v)",
		serviceOrContainer, project, known)
}
