// Package localnet orchestrates `canton-devkit localnet ...` subcommands.
// The actual Canton/Splice processes run in containers — this package
// only composes preflight + Splice fetcher + docker-compose runner +
// per-major adapter + state registry into a coherent UX.
package localnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// Exit codes. Stable across releases — scripts can depend on them.
const (
	ExitSuccess        = 0
	ExitUserError      = 1
	ExitPreflightFail  = 2
	ExitTimeout        = 3
	ExitRuntimeFailure = 4
)

// ExitCodeError carries a deterministic process exit code through a
// Cobra `RunE` return. Cobra's default behavior collapses every error
// into exit 1; we wrap our codes in this type so the App.Run layer can
// extract them via errors.As.
//
// The string form is only used when something accidentally treats this
// as a normal error — App.Run intercepts ExitCodeError before its
// generic stderr-print path runs, so subcommands own all human-visible
// stderr output.
type ExitCodeError int

func (e ExitCodeError) Error() string { return fmt.Sprintf("exit code %d", int(e)) }

// AsExitError converts a non-zero process exit code returned by one of
// our Run* functions into an error suitable for Cobra's RunE return.
// Zero returns nil so the happy path doesn't allocate.
func AsExitError(code int) error {
	if code == 0 {
		return nil
	}
	return ExitCodeError(code)
}

// UpOptions captures the parsed `localnet up` flags. Cobra binds the
// flags directly to the corresponding `cmd.Flags().StringVar` targets in
// internal/cli/localnet/up.go; this struct exists so the Run* function
// has a single typed entry point.
type UpOptions struct {
	Name    string
	Version string // "" or "latest" → splice.LatestAlias

	// SkipPreflight bypasses the docker.RunPreflight call. This is a
	// test-only knob — unit tests for the `up` orchestration can't run
	// Docker checks in CI. Not exposed as a CLI flag.
	SkipPreflight bool
}

// ValidateName returns an error if the supplied --name is empty or
// fails the DNS-label rule. Thin wrapper over registry.ValidateName
// — Zhe flagged in PR #20 that CLI and registry validation must
// share a single rule (otherwise we maintain two policies that
// drift). The empty-string check is hoisted here so the CLI error
// message can mention `--name` rather than the generic
// ErrInvalidName phrasing.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if err := registry.ValidateName(name); err != nil {
		return fmt.Errorf("--name %w", err)
	}
	return nil
}

// RunUp orchestrates the full bring-up sequence:
//
//  1. Resolve --version against the curated list + look up the per-major
//     adapter for that Splice tag.
//  2. Acquire the per-instance lock so concurrent ups on the same name
//     fail fast.
//  3. Run preflight (Docker CLI, daemon, Compose v2, ports the adapter
//     demands, disk, memory, host prereqs) with platform-specific
//     remediation.
//  4. Fetch + verify Splice LocalNet compose project (cache-hit fast
//     path).
//  5. Persist initial state.json so a crash mid-bring-up leaves enough
//     metadata for `localnet down` to clean up.
//  6. `docker compose up -d --wait` with the adapter-supplied compose
//     files / env files / profiles, and shell-exported overlay env.
//  7. Mark state running, print endpoints + state file paths.
//
// SIGINT/SIGTERM cancel the in-flight `docker compose` call. RunUp never
// modifies the host outside ~/.canton/.
func RunUp(ctx context.Context, out io.Writer, errw io.Writer, opts *UpOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1. Resolve version + adapter.
	version, err := splice.Resolve(opts.Version)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	adapter, err := adapterFor(version)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitRuntimeFailure
	}

	_, _ = fmt.Fprintf(out, "Starting Canton LocalNet %q (Splice %s, adapter %s)...\n",
		opts.Name, version.Tag, adapter.MajorVersion())

	// 2. Per-instance lock. Released on any return path.
	release, err := registry.Lock(opts.Name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	defer release()

	// 3. Preflight (Docker CLI / daemon / Compose v2 / disk / memory).
	// Host TCP ports are NOT preflight-checked — DevKit allocates them
	// ephemerally (step 8) so port availability is never a precondition.
	// SkipPreflight is honored for unit tests; in production code paths
	// the flag is always false.
	if !opts.SkipPreflight {
		_, _ = fmt.Fprintln(out, "Running preflight checks...")
		report := docker.RunPreflight(ctx, docker.Options{
			DataDir:        registry.Root(),
			MinDiskBytes:   10 * 1024 * 1024 * 1024,
			MinMemoryBytes: 4 * 1024 * 1024 * 1024,
		})
		report.Write(out)
		if !report.OK() {
			_, _ = fmt.Fprintln(errw, "\nPreflight failed. Address the items above and re-run.")
			return ExitPreflightFail
		}
	}

	// 5. Fetch + verify compose project.
	cacheRoot := splice.CacheRoot()
	projectDir, err := splice.Fetch(ctx, version, cacheRoot, out)
	if err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Interrupted while fetching Splice LocalNet")
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "Failed to fetch Splice LocalNet %s: %s\n", version.Tag, err)
		return ExitRuntimeFailure
	}

	// 6. Generate per-instance container-rename overlay. Every Splice
	// service has a hardcoded container_name; without renaming, two
	// instances collide daemon-wide regardless of project name.
	dataDir := registry.DataDirFor(opts.Name)
	containerPrefix := opts.Name + "-"
	overlayPath, err := WriteContainerRenameOverlay(dataDir, containerPrefix)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to write container-rename overlay: %s\n", err)
		return ExitRuntimeFailure
	}

	// 7. Persist state BEFORE compose-up so a crash mid-bring-up still
	// leaves enough metadata for `localnet down` to clean up.
	composeFiles := append([]string(nil), adapter.ComposeFiles()...)
	composeFiles = append(composeFiles, overlayPath) // absolute path; not relative to projectDir

	// PR #20 #5/#7: read any pre-existing state so we can reuse the
	// previously assigned UI host ports — stable URLs across an
	// up/down/up cycle is the whole point of persisting them. If no
	// prior state exists (first run, or it was deleted), priorPorts
	// stays nil and ReuseOrAllocateUIPorts falls back to full
	// ephemeral allocation.
	var priorPorts map[string]int
	if prior, err := registry.Read(opts.Name); err == nil {
		priorPorts = prior.Ports
	}

	state := registry.NewState(opts.Name, version.Tag)
	state.ComposeProject = "canton-" + opts.Name
	state.ComposeFiles = composeFiles
	state.DockerNetwork = opts.Name
	state.ContainerPrefix = containerPrefix
	state.ProjectDir = projectDir
	state.DataDir = dataDir
	state.Ports = adapter.EndpointMap() // overwritten post-up if ephemeral
	state.AlphaProtocolEnabled = adapter.SupportsAlphaProtocol()
	state.Status = registry.StatusCreating
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to write registry state: %s\n", err)
		return ExitRuntimeFailure
	}

	// 8. Pre-allocate UI host ports. Splice's nginx/swagger/postgres
	// services don't honor TEST_PORT — they only consume the per-service
	// env vars (APP_USER_UI_PORT etc).
	//
	// On first run priorPorts is nil → all ports allocated ephemerally.
	// On re-up after a clean `down`, ReuseOrAllocateUIPorts hands back
	// the same ports the user previously had (PR #20 #5/#7 stable-URL
	// contract) unless one is busy, in which case we surface
	// ErrPortBusy as a user error and stop — silently picking a new
	// port would defeat the contract.
	uiOverrides, err := ReuseOrAllocateUIPorts(UIPortEnvVars(), priorPorts)
	if err != nil {
		if errors.Is(err, ErrPortBusy) {
			_, _ = fmt.Fprintf(errw,
				"%s\nFree the conflicting process (lsof -i :<port>) or tear down the "+
					"other instance and re-run `localnet up --name %s`.\n",
				err, opts.Name)
			return ExitPreflightFail
		}
		_, _ = fmt.Fprintf(errw, "Failed to allocate UI ports: %s\n", err)
		return ExitRuntimeFailure
	}
	state.Ports["app_user_ui"] = uiOverrides["APP_USER_UI_PORT"]
	state.Ports["app_provider_ui"] = uiOverrides["APP_PROVIDER_UI_PORT"]
	state.Ports["sv_ui"] = uiOverrides["SV_UI_PORT"]
	state.Ports["swagger_ui"] = uiOverrides["SWAGGER_UI_PORT"]
	state.Ports["postgres"] = uiOverrides["DB_PORT"]

	// Build the compose process env and run `up -d --wait`. Ephemeral
	// is always true: canton participant ports get TEST_PORT="" and
	// UI/postgres ports come from uiOverrides.
	params := splice.InstanceParams{
		Name:            opts.Name,
		Version:         version,
		ProjectDir:      projectDir,
		Ephemeral:       true,
		UIPortOverrides: uiOverrides,
	}
	env := append(os.Environ(), mapToEnv(adapter.OverlayEnv(params))...)

	runner := &docker.ComposeRunner{
		ProjectName:  state.ComposeProject,
		ComposeFiles: composeFiles,
		EnvFiles:     adapter.EnvFiles(),
		Env:          env,
		WorkDir:      projectDir,
		LogWriter:    out,
	}

	_, _ = fmt.Fprintln(out, "Starting services...")
	if err := runner.Up(ctx); err != nil {
		markFailed(state, errw)
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Interrupted while starting services")
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "Failed to start services: %s\n", err)
		return ExitRuntimeFailure
	}

	_, _ = fmt.Fprintln(out, "Waiting for services to become healthy...")
	if err := runner.WaitForHealthy(ctx); err != nil {
		markFailed(state, errw)
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Timed out waiting for services")
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "Services failed health check: %s\n", err)
		return ExitRuntimeFailure
	}

	// 9. Capture JWTs and persist running state. (UI ports were
	// pre-allocated in step 8; canton participant ports run on Docker-
	// ephemeral host ports and aren't surfaced — they're network-
	// internal.)
	if creds := captureCredentials(projectDir, errw); creds != nil {
		state.Credentials = creds
	}
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: services healthy but registry write failed: %s\n", err)
	}

	_, _ = fmt.Fprintf(out, "\nCanton LocalNet %q (Splice %s) is ready.\n\n",
		opts.Name, version.Tag)
	printEndpoints(out, state)
	return ExitSuccess
}

// captureCredentials reads the per-role auth env files from the cached
// project and signs a JWT for each role. Returns nil + a warning on the
// error path — JWT capture failure shouldn't fail an otherwise-healthy
// bring-up.
func captureCredentials(projectDir string, errw io.Writer) map[string]registry.Credential {
	inputs, err := splice.LoadCredentialInputs(projectDir)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not read auth env files: %s\n", err)
		return nil
	}
	// One-shot dev-secret warning to the injected stderr. SignToken
	// itself is pure (no global writes) — the orchestrator owns the
	// output channel. RunCreds emits the same warning when re-issuing
	// tokens from a previously-captured instance.
	if len(inputs) > 0 {
		_, _ = fmt.Fprintln(errw, splice.DevSecretWarning)
	}
	out := make(map[string]registry.Credential, len(inputs))
	for _, in := range inputs {
		jwt, err := splice.SignToken(in)
		if err != nil {
			_, _ = fmt.Fprintf(errw, "Warning: JWT sign failed for %s: %s\n", in.Role, err)
			continue
		}
		out[string(in.Role)] = registry.Credential{
			Role:     string(in.Role),
			User:     in.User,
			Audience: in.Audience,
			JWT:      jwt,
		}
	}
	return out
}

func markFailed(state *registry.State, errw io.Writer) {
	state.Status = registry.StatusFailed
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not record failure in registry: %s\n", err)
	}
}

func mapToEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func printEndpoints(out io.Writer, state *registry.State) {
	_, _ = fmt.Fprintln(out, "Endpoints:")
	for _, e := range orderedEndpointKeys() {
		if port, ok := state.Ports[e.key]; ok {
			_, _ = fmt.Fprintf(out, "  %-20s %s://localhost:%d\n", e.label+":", e.scheme, port)
		}
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Instance files:")
	_, _ = fmt.Fprintf(out, "  State:           %s\n", registry.PathFor(state.Name))
	_, _ = fmt.Fprintf(out, "  Compose project: %s\n", state.ProjectDir)
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Stop with: canton-devkit localnet down --name %s\n", state.Name)
}

// orderedEndpointKeys returns the endpoint pretty-print order — stable
// regardless of map iteration order.
func orderedEndpointKeys() []endpointDisplay {
	return []endpointDisplay{
		{"app_user_ui", "App User UI", "http"},
		{"app_provider_ui", "App Provider UI", "http"},
		{"sv_ui", "Super Validator UI", "http"},
		{"swagger_ui", "Swagger UI", "http"},
		{"postgres", "Postgres", "postgresql"},
	}
}

type endpointDisplay struct {
	key    string
	label  string
	scheme string
}
