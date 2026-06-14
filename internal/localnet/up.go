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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
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

// composeOps is the subset of *docker.ComposeRunner that RunUp drives.
// Extracted as an interface (and matched implicitly by ComposeRunner)
// so a fake-driven happy-path test can swap in a no-op stub. Two
// methods is the floor — adding more here means rewiring RunUp first,
// keeping the seam honest.
type composeOps interface {
	Up(ctx context.Context) error
	WaitForHealthy(ctx context.Context) error
}

// UpOptions captures the parsed `localnet up` flags. Cobra binds the
// flags directly to the corresponding `cmd.Flags().StringVar` targets in
// internal/cli/localnet/up.go; this struct exists so the Run* function
// has a single typed entry point.
type UpOptions struct {
	Name    string
	Version string // "" or "latest" → splice.LatestAlias

	// AllowUncurated opts the user into layer 2 of the two-layer
	// version model: if --version is not in the curated
	// catalogue, RunUp consults the upstream Splice GitHub repo to
	// resolve a commit SHA, prints a one-line "uncurated" warning, and
	// proceeds. Without this flag, an uncurated --version fails fast
	// with ErrUncuratedTag. Default false keeps the audited path the
	// safe default for first-time users.
	AllowUncurated bool

	// Profiles is the docker compose profile set to enable on this
	// bring-up. `--profile observability` adds Prometheus +
	// Grafana via the assets/compose/observability.yaml overlay.
	// Empty (default) means "no opt-in profiles" — same behavior
	// as before this field existed.
	Profiles []string

	// PortBase, when > 0, switches host-port assignment from automatic
	// (ephemeral, stable-reuse) to DETERMINISTIC: each service is pinned
	// to PortBase+N (see DeriveUIPorts). Used for reproducible
	// multi-instance / CI layouts where a fixed, predictable port map
	// matters. Every derived port must be free, or RunUp fails with a
	// PortBaseConflict (no silent fallback). 0 (default) keeps the
	// auto-allocation behavior.
	PortBase int

	// SkipPreflight bypasses the docker.RunPreflight call. This is a
	// test-only knob — unit tests for the `up` orchestration can't run
	// Docker checks in CI. Not exposed as a CLI flag.
	SkipPreflight bool

	// Test-only seams. When nil, RunUp uses the real
	// splice.Fetch and *docker.ComposeRunner. The fake-driven happy-
	// path test in up_test.go injects no-op stubs so we can prove the
	// orchestration sequence without docker / network.
	//
	// FetchFn replaces splice.Fetch — return a directory that contains
	// (at minimum) an env/ subdir with role auth files; nil err means
	// "happy path."
	FetchFn func(ctx context.Context, v splice.Version, cacheRoot string, out io.Writer) (string, error)
	// NewRunner replaces the *docker.ComposeRunner constructor —
	// receives the same plumbing fields the real runner gets so the
	// test can assert on them. Returning a stub that succeeds on
	// Up+WaitForHealthy drives RunUp to the success path.
	NewRunner func(projectName string, composeFiles, envFiles, env []string, workDir string, logw io.Writer) composeOps
}

// ValidateName returns an error if the supplied --name is empty or
// fails the DNS-label rule. Thin wrapper over registry.ValidateName
// so CLI and registry validation share a single rule (otherwise we
// maintain two policies that drift). The empty-string check is
// hoisted here so the CLI error message can mention `--name` rather
// than the generic ErrInvalidName phrasing.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if err := registry.ValidateName(name); err != nil {
		return fmt.Errorf("--name %w", err)
	}
	return nil
}

// RunUp orchestrates the full bring-up sequence. The Step taxonomy
// (StepResolveVersion through StepCaptureJWTs in progress.go) matches
// the eight phases the webui-create.jsx mockup renders; each phase
// calls into the `prog` Progress interface so the CLI's TextProgress
// emits today's terminal lines while the Web UI's SSEProgress impl
// ships typed step events to the browser.
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
//
// CLI byte-equivalence: the CLI caller passes `&TextProgress{OutW:
// cmd.OutOrStdout(), ErrW: cmd.ErrOrStderr()}`. TextProgress filters
// StartStep to a three-step allowlist (preflight / start_services /
// wait_healthy) so today's terminal output is unchanged; the five
// silent steps become non-silent only when SSEProgress is wired in.
func RunUp(ctx context.Context, prog Progress, opts *UpOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Reject unknown --profile values up front. Shared with the
	// Web UI create handler via ValidateProfiles so a typo'd
	// `--profile observabilty` fails fast with a user error here
	// instead of silently producing a metric-less instance — the
	// exact failure the UI's allowlist already prevents with a 400.
	// A pure input check: runs before the lock / any side effect.
	if err := ValidateProfiles(opts.Profiles); err != nil {
		prog.FailStep(StepResolveVersion, err.Error(), nil)
		return ExitUserError
	}

	// Wall-clock start for the welcome screen's "ready in Nm Ns" line.
	// Captured at the top of RunUp so it includes resolve+preflight
	// time, not just compose-up time — gives the user a realistic
	// "cold start cost" expectation for their next dev cycle.
	startedAt := time.Now()

	// 1. Resolve version + adapter. Layer-1 (curated) is the default;
	// AllowUncurated opts into layer-2 (upstream resolution) — see
	// internal/splice/resolver.go.
	prog.StartStep(StepResolveVersion, "")
	version, fromUpstream, err := splice.ResolveOrUpstream(ctx, opts.Version, opts.AllowUncurated)
	if err != nil {
		prog.FailStep(StepResolveVersion, err.Error(), nil)
		return ExitUserError
	}
	if fromUpstream {
		// One-line caveat so the user is never surprised that the bits
		// they're running weren't reviewed by a DevKit maintainer.
		prog.Warn(fmt.Sprintf(
			"using uncurated Splice tag %q (resolved upstream to commit %s); not tested by DevKit",
			version.Tag, shortSHA(version.Commit)))
	}
	// Alpha-channel warning. Catalogue entries marked `channel: "alpha"`
	// are opt-in pre-releases (e.g. the Token Standard V2 snapshot) —
	// images come from a different ghcr registry and the upstream
	// branch can be reset on a weekly cadence. Surface this once at
	// startup so it shows up in CI logs alongside the version banner.
	if version.IsAlpha() {
		prog.Warn(fmt.Sprintf(
			"selecting alpha-channel Splice %q (pre-release; expect upstream resets and additional configuration — see docs/versions.md)",
			version.Tag))
	}
	adapter, err := adapterFor(version)
	if err != nil {
		prog.FailStep(StepResolveVersion, err.Error(), nil)
		return ExitRuntimeFailure
	}
	prog.FinishStep(StepResolveVersion,
		fmt.Sprintf("splice %s · adapter %s", version.Tag, adapter.MajorVersion()))

	// The "Starting Canton LocalNet ... " header preserves the
	// existing CLI line. TextProgress writes it verbatim; SSEProgress
	// gets it as a console-style event so the browser sees the same
	// banner.
	_, _ = fmt.Fprintf(prog.Out(), "Starting Canton LocalNet %q (Splice %s, adapter %s)...\n",
		opts.Name, version.Tag, adapter.MajorVersion())

	// 2. Per-instance lock. Released on any return path.
	prog.StartStep(StepAcquireLock, "")
	release, err := registry.Lock(opts.Name)
	if err != nil {
		prog.FailStep(StepAcquireLock, err.Error(), nil)
		return ExitUserError
	}
	defer release()
	prog.FinishStep(StepAcquireLock, "")

	// 3. Preflight (Docker CLI / daemon / Compose v2 / disk / memory).
	// Host TCP ports are NOT preflight-checked — DevKit allocates them
	// ephemerally (step 8) so port availability is never a precondition.
	// SkipPreflight is honored for unit tests; in production code paths
	// the flag is always false.
	if !opts.SkipPreflight {
		prog.StartStep(StepPreflight, "")
		// Memory floor is version-aware (CLI ↔ Web UI parity:
		// matches GET /api/preflight). splice.MinMemoryFor() is
		// guaranteed to return >= docker.DefaultMinMemoryBytes
		// by TestThresholdParity_VersionMinAtLeastDefault — a
		// per-version override can only RAISE the gate, never
		// weaken it, so the `doctor && up` contract holds:
		// every host that passes doctor still passes up's floor
		// for the lightest catalogued version.
		report := docker.RunPreflight(ctx, docker.Options{
			DataDir:                registry.Root(),
			MinDiskBytes:           docker.DefaultMinDiskBytes,
			MinMemoryBytes:         splice.MinMemoryFor(version),
			RecommendedMemoryBytes: splice.RecommendedMemoryFor(version),
		})
		// Anonymous telemetry (no-op unless enabled): the docker engine
		// flavor + compose-version bucket discovered by preflight. No host,
		// path, or version string is recorded — only the allow-listed bucket.
		telemetry.Inc("dpm/docker_engine", telemetry.NormEngine(report.DockerEngine))
		telemetry.Inc("dpm/compose_version_bucket", telemetry.ComposeBucket(report.ComposeVersion))
		report.Write(prog.Out())
		if !report.OK() {
			// Stamp the most-specific code we can infer from the
			// failed checks so the UI can render a
			// targeted remediation panel (Docker not running vs
			// memory too low vs disk full, etc.) instead of a
			// generic "preflight failed" wall of text.
			prog.FailStep(StepPreflight,
				"\nPreflight failed. Address the items above and re-run.",
				WithCode(fmt.Errorf("preflight failed"), PreflightCodeFromReport(report)))
			return ExitPreflightFail
		}
		prog.FinishStep(StepPreflight, "")
	}

	// 4. Fetch + verify compose project. FetchFn is a test seam;
	// in production it's nil and we call splice.Fetch directly.
	prog.StartStep(StepFetchSplice, "")
	cacheRoot := splice.CacheRoot()
	fetch := opts.FetchFn
	if fetch == nil {
		fetch = splice.Fetch
	}
	projectDir, err := fetch(ctx, version, cacheRoot, prog.Out())
	if err != nil {
		if ctx.Err() != nil {
			prog.FailStep(StepFetchSplice, "Interrupted while fetching Splice LocalNet", nil)
			return ExitTimeout
		}
		prog.FailStep(StepFetchSplice,
			fmt.Sprintf("Failed to fetch Splice LocalNet %s", version.Tag), err)
		return ExitRuntimeFailure
	}
	prog.FinishStep(StepFetchSplice, "")

	// 5. Persist state — generate per-instance container-rename
	// overlay, write the (creating) state.json. Every Splice service
	// has a hardcoded container_name; without renaming, two instances
	// collide daemon-wide regardless of project name.
	prog.StartStep(StepPersistState, "")
	dataDir := registry.DataDirFor(opts.Name)
	containerPrefix := opts.Name + "-"
	overlayPath, err := WriteContainerRenameOverlay(dataDir, containerPrefix)
	if err != nil {
		prog.FailStep(StepPersistState, "Failed to write container-rename overlay", err)
		return ExitRuntimeFailure
	}

	composeFiles := append([]string(nil), adapter.ComposeFiles()...)
	composeFiles = append(composeFiles, overlayPath) // absolute path; not relative to projectDir

	// Force all published ports to bind to 127.0.0.1 (loopback only).
	// The upstream Splice compose.yaml binds ports without a host IP,
	// so Docker defaults to 0.0.0.0 — exposing unauthenticated gRPC,
	// database, and HTTP endpoints to the local network. This overlay
	// uses Compose's !override tag (v2.24.0+) to replace the port
	// lists with 127.0.0.1-prefixed bindings.
	loopbackPath, err := WriteLoopbackPortsOverlay(dataDir)
	if err != nil {
		prog.FailStep(StepPersistState, "Failed to write loopback-ports overlay", err)
		return ExitRuntimeFailure
	}
	composeFiles = append(composeFiles, loopbackPath)

	// Observability profile(s) materialize the embedded Prometheus +
	// Grafana overlay and append its compose file. Per-component
	// profiles (`prometheus`, `grafana`) and the legacy umbrella
	// (`observability` = both) all funnel through
	// ExpandObservabilityProfiles so the rest of the bring-up logic
	// only deals with two booleans.
	wantPrometheus, wantGrafana := ExpandObservabilityProfiles(opts.Profiles)
	hasObservabilityProfile := wantPrometheus || wantGrafana
	if wantGrafana && !wantPrometheus {
		// Grafana without a bundled Prometheus is valid (user may
		// point it at an external scrape source) but is a common
		// mistake — surface a warning so an empty dashboard is
		// expected rather than mysterious.
		prog.Warn("--profile grafana enabled without --profile prometheus: " +
			"Grafana will start with no bundled data source. Add " +
			"--profile prometheus, or configure an external Prometheus " +
			"manually before relying on the dashboards.")
	}
	if hasObservabilityProfile {
		overlay, err := MaterializeObservabilityOverlay(dataDir, projectDir, prog.Err())
		if err != nil {
			prog.FailStep(StepPersistState,
				"Failed to extract observability overlay", err)
			return ExitRuntimeFailure
		}
		composeFiles = append(composeFiles, overlay)
	}

	// tokens-v2 profile materializes the alpha-protocol
	// Canton config overlay. Required when running the Token Standard
	// V2 snapshot (alpha catalogue entry); a stable Splice version
	// with this profile on is harmless — it just injects an env that
	// the stable Canton ignores.
	hasTokensV2Profile := false
	for _, p := range opts.Profiles {
		if p == TokensV2ProfileName {
			hasTokensV2Profile = true
			break
		}
	}
	if hasTokensV2Profile {
		overlay, err := MaterializeTokensV2Overlay(dataDir, prog.Err())
		if err != nil {
			prog.FailStep(StepPersistState,
				"Failed to extract tokens-v2 overlay", err)
			return ExitRuntimeFailure
		}
		composeFiles = append(composeFiles, overlay)
	} else if version.IsAlpha() {
		// Alpha version selected without the matching profile — Canton
		// won't carry the protocol-35 flags V2 requires and the bring-up
		// is highly likely to fail. WARN loudly rather than refuse, so
		// users who pin alpha for non-V2 reasons (e.g. previewing other
		// changes on the snapshot) aren't blocked.
		prog.Warn(fmt.Sprintf(
			"alpha Splice %q selected without --profile %s — Canton "+
				"will not enable initial-protocol-version=35; V2 features "+
				"WILL fail. Re-run with --profile %s to inject the alpha-protocol config.",
			version.Tag, TokensV2ProfileName, TokensV2ProfileName))
	}

	// Read any pre-existing state so we can reuse the
	// previously assigned UI host ports — stable URLs across an
	// up/down/up cycle is the whole point of persisting them. If no
	// prior state exists (first run, or it was deleted), priorPorts
	// stays nil and ReuseOrAllocateUIPorts falls back to full
	// ephemeral allocation.
	//
	// We also stash the prior run's recorded image digests so the
	// post-up capture can WARN if a republished ghcr tag changed what
	// runs. Only meaningful when the SAME version is being
	// brought back up — a version change legitimately swaps images, so
	// we skip the comparison in that case.
	var priorPorts map[string]int
	var priorImageDigests map[string]string
	if prior, err := registry.Read(opts.Name); err == nil {
		// Refuse to re-up an instance that is already running. Without
		// this guard RunUp overwrites the live state with
		// Status=creating and then fails port allocation (the published
		// host ports are still held), stranding the healthy instance as
		// a permanent `creating` zombie the reconciler won't heal and
		// `scrub` would orphan. Mirrors the Web UI's 409 INSTANCE_RUNNING
		// guard.
		if prior.Status == registry.StatusRunning {
			prog.FailStep(StepPersistState, fmt.Sprintf(
				"instance %q is already running — stop it first with "+
					"`localnet down --name %s`, or bounce it with `localnet restart --name %s`",
				opts.Name, opts.Name, opts.Name), nil)
			return ExitUserError
		}
		priorPorts = prior.Ports
		if prior.SpliceVersion == version.Tag {
			priorImageDigests = prior.ImageDigests
		}
	}

	// The enabled profile set: the adapter's base profiles (which gate
	// every core Splice service) plus the user's `--profile` opt-ins.
	// Persisted in state and re-used for the `up` runner below so the
	// recorded set is byte-identical to what we actually bring up;
	// `restart`/`pause`/`status` replay it (teardown does not need it —
	// see ComposeRunner.Stop).
	enabledProfiles := append(adapter.Profiles(), opts.Profiles...)

	state := registry.NewState(opts.Name, version.Tag)
	state.ComposeProject = "canton-" + opts.Name
	state.ComposeFiles = composeFiles
	state.Profiles = enabledProfiles
	state.DockerNetwork = opts.Name
	state.ContainerPrefix = containerPrefix
	state.ProjectDir = projectDir
	state.DataDir = dataDir
	state.Ports = adapter.EndpointMap() // overwritten post-up if ephemeral
	state.AlphaProtocolEnabled = adapter.SupportsAlphaProtocol()
	state.Status = registry.StatusCreating
	if err := registry.Write(state); err != nil {
		prog.FailStep(StepPersistState, "Failed to write registry state", err)
		return ExitRuntimeFailure
	}

	// Pre-allocate UI host ports. Splice's nginx/swagger/postgres
	// services don't honor TEST_PORT — they only consume the per-service
	// env vars (APP_USER_UI_PORT etc).
	//
	// On first run priorPorts is nil → all ports allocated ephemerally.
	// On re-up after a clean `down`, ReuseOrAllocateUIPorts hands back
	// the same ports the user previously had (stable-URL contract)
	// unless one is busy, in which case we surface ErrPortBusy as
	// a user error and stop — silently picking a new port would
	// defeat the contract.
	//
	// When --profile observability is on, allocate Prometheus +
	// Grafana host ports alongside the rest so they go through the
	// stable-reuse contract too.
	portEnvVars := UIPortEnvVars()
	if wantPrometheus {
		portEnvVars = append(portEnvVars, "PROMETHEUS_HOST_PORT")
	}
	if wantGrafana {
		portEnvVars = append(portEnvVars, "GRAFANA_HOST_PORT")
	}
	// Explicit-port mode (--port-base) pins deterministic ports and
	// fails on any conflict; default mode auto-allocates with stable
	// reuse across re-ups.
	var uiOverrides map[string]int
	if opts.PortBase > 0 {
		uiOverrides, err = DeriveUIPorts(portEnvVars, opts.PortBase)
	} else {
		uiOverrides, err = ReuseOrAllocateUIPorts(portEnvVars, priorPorts)
	}
	if err != nil {
		var conflict *PortBaseConflict
		if errors.Is(err, ErrPortBusy) || errors.As(err, &conflict) {
			// Stamp PORTS_IN_USE so the frontend can
			// render the "free the port" remediation panel
			// instead of the generic failure dialog.
			prog.FailStep(StepPersistState,
				fmt.Sprintf("%s\nFree the conflicting process (lsof -i :<port>), pick a different --port-base, or tear down the "+
					"other instance and re-run `localnet up --name %s`.", err, opts.Name),
				WithCode(err, ErrCodePortsInUse))
			return ExitPreflightFail
		}
		prog.FailStep(StepPersistState, "Failed to allocate UI ports", err)
		return ExitRuntimeFailure
	}
	state.Ports["app_user_ui"] = uiOverrides["APP_USER_UI_PORT"]
	state.Ports["app_provider_ui"] = uiOverrides["APP_PROVIDER_UI_PORT"]
	state.Ports["sv_ui"] = uiOverrides["SV_UI_PORT"]
	state.Ports["swagger_ui"] = uiOverrides["SWAGGER_UI_PORT"]
	state.Ports["postgres"] = uiOverrides["DB_PORT"]
	if wantPrometheus {
		state.Ports["prometheus_ui"] = uiOverrides["PROMETHEUS_HOST_PORT"]
	}
	if wantGrafana {
		state.Ports["grafana_ui"] = uiOverrides["GRAFANA_HOST_PORT"]
	}
	prog.FinishStep(StepPersistState, "")

	// 6. Starting services. Build the compose env files and run
	// `up -d --wait`. Ephemeral is always true: canton participant
	// ports get TEST_PORT="" and UI/postgres ports come from
	// uiOverrides.
	params := splice.InstanceParams{
		Name:            opts.Name,
		Version:         version,
		ProjectDir:      projectDir,
		Ephemeral:       true,
		UIPortOverrides: uiOverrides,
	}
	overlayVars := adapter.OverlayEnv(params)

	// Persist the overlay env to a .env file so downstream commands
	// (down, logs, pause, restart, clean) replay the exact same
	// compose environment via --env-file — no adapter re-derivation
	// needed. The file is written to DataDir alongside state.json.
	overlayEnvPath, err := WriteOverlayEnv(dataDir, overlayVars)
	if err != nil {
		prog.FailStep(StepPersistState, "Failed to write overlay env", err)
		return ExitRuntimeFailure
	}

	// Env files: adapter base files + the generated overlay (last wins).
	envFiles := append(adapter.EnvFiles(), overlayEnvPath)

	// NewRunner is a test seam. Production: build the
	// real *docker.ComposeRunner (which satisfies composeOps
	// implicitly).
	var runner composeOps
	if opts.NewRunner != nil {
		runner = opts.NewRunner(state.ComposeProject, composeFiles, envFiles, nil, projectDir, prog.Out())
	} else {
		runner = &docker.ComposeRunner{
			ProjectName:  state.ComposeProject,
			ComposeFiles: composeFiles,
			EnvFiles:     envFiles,
			WorkDir:      projectDir,
			LogWriter:    prog.Out(),
			// docker compose v5 treats CLI --profile as REPLACING (not
			// augmenting) COMPOSE_PROFILES from the env, so an
			// `up --profile tokens-v2` invocation activates only
			// tokens-v2 and every base service (sv / app-provider /
			// app-user / swagger-ui) is filtered out → "no service
			// selected". Merge the adapter's base profiles with the
			// user's opt-ins on the CLI so the union is what docker
			// compose sees. Persisted as state.Profiles above so
			// restart/pause/status replay the identical set.
			Profiles: enabledProfiles,
		}
	}

	prog.StartStep(StepStartServices, "")
	if err := runner.Up(ctx); err != nil {
		markFailed(state, prog.Err())
		if ctx.Err() != nil {
			prog.FailStep(StepStartServices, "Interrupted while starting services", nil)
			return ExitTimeout
		}
		prog.FailStep(StepStartServices, "Failed to start services", err)
		return ExitRuntimeFailure
	}
	prog.FinishStep(StepStartServices, "")

	// 7. Wait for services to become healthy.
	prog.StartStep(StepWaitHealthy, "")
	if err := runner.WaitForHealthy(ctx); err != nil {
		markFailed(state, prog.Err())
		if ctx.Err() != nil {
			prog.FailStep(StepWaitHealthy, "Timed out waiting for services", nil)
			return ExitTimeout
		}
		// Try to diagnose the failure by inspecting the
		// unhealthy containers' recent logs. Most common cause on
		// first-run with default Docker memory: canton JVM OOM-loop
		// — its logs include "exceeds half of the container's
		// total memory". If we spot that pattern, stamp
		// CANTON_OOM so the UI renders the memory-raise
		// remediation card; otherwise stamp CONTAINER_UNHEALTHY
		// so the UI can at least offer a "see logs" affordance.
		diagCtx, cancelDiag := context.WithTimeout(ctx, 15*time.Second)
		code := diagnoseUnhealthy(diagCtx, state.ComposeProject)
		cancelDiag()
		if code == "" {
			code = ErrCodeContainerUnhealthy
		}
		prog.FailStep(StepWaitHealthy, "Services failed health check",
			WithCode(err, code))
		return ExitRuntimeFailure
	}
	prog.FinishStep(StepWaitHealthy, "")

	// 8. Capture Canton participant ports. The Canton
	// container exposes Ledger/Admin/JSON APIs per party role on
	// Docker-ephemeral host ports — we ask docker what they ended
	// up as and persist them so the Web UI (Explorer, DAR Manager,
	// future M3 token surfaces) can dial without a manual
	// `--admin-host=localhost:<port>` flag. Best-effort: any port
	// that fails to query is silently omitted, not stamped as 0.
	for key, port := range CaptureCantonPorts(ctx, state.ComposeProject) {
		state.Ports[key] = port
	}
	// Capture the canton/splice :10013 metrics ports too, so the shared
	// host-level Prometheus can scrape this instance via
	// host.docker.internal:<port> once it's registered.
	for key, port := range CaptureMetricsPorts(ctx, state.ComposeProject) {
		state.Ports[key] = port
	}

	// 8b. Capture the content digests of the images that actually ran
	// and persist them. The catalogue pins the source tree but ghcr
	// image tags are mutable; recording digests lets a later up/restart
	// detect a republished tag. Best-effort — a capture failure
	// just skips drift detection, never fails the bring-up. If the same
	// version was up before with recorded digests, warn on any change.
	if digests := CaptureImageDigests(ctx, state.ComposeProject); len(digests) > 0 {
		if changed := DiffImageDigests(priorImageDigests, digests); len(changed) > 0 {
			prog.Warn(fmt.Sprintf(
				"Splice image digest(s) changed since the last up of %q — a mutable ghcr tag was republished; "+
					"verify this is expected: %s",
				version.Tag, strings.Join(changed, ", ")))
		}
		state.ImageDigests = digests
	}

	// 9. Capture JWTs and persist running state. (UI ports were
	// pre-allocated in step 5; Canton participant gRPC/JSON API
	// ports were just captured in step 8.)
	prog.StartStep(StepCaptureJWTs, "")
	if creds := captureCredentials(projectDir, prog.Err()); creds != nil {
		state.Credentials = creds
	}
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		prog.Warn(fmt.Sprintf("services healthy but registry write failed: %s", err))
	}
	prog.FinishStep(StepCaptureJWTs, "")

	// Register this instance with the shared host-level observability
	// stack and ensure the stack is up, so one Prometheus+Grafana
	// scrapes every instance. Register+ensure run atomically under the
	// shared-stack lock so a concurrent teardown can't race us. Best-effort
	// — a failure here is a warning, never a failed bring-up. (The
	// per-instance overlay still runs alongside the shared stack; gating it
	// off is a deferred decision — see docs/observability.md.)
	if hasObservabilityProfile {
		if _, err := RegisterInstanceAndEnsureStack(ctx, state, prog.Err()); err != nil {
			prog.Warn("shared observability: " + err.Error())
		}
	}

	// Welcome screen — replaces the old inline brand Box + plain
	// endpoint listing with a single composable view (lockup +
	// primary CTA + grouped endpoint cards + try-next cheat sheet).
	// The renderer auto-degrades to a plain text variant on non-TTY
	// writers so `localnet up | tee log` still grep-cleanly.
	renderWelcome(prog.Out(), opts.Name, version.Tag, state, time.Since(startedAt))

	// Emit the terminal `done` event. On the CLI (TextProgress) Done("")
	// is a no-op — renderWelcome already printed the success box. On the
	// Web UI (SSEProgress) this is the ONLY signal that flips the create/
	// resume/restart modal out of its "running" state: without it the
	// modal hangs forever on every successful bring-up, never refreshes
	// the dashboard, and can't be dismissed via Esc/backdrop.
	prog.Done("")
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

// renderWelcome composes the post-ready welcome screen using the term
// package's WelcomeScreen primitive (see internal/ui/term/welcome.go).
//
// Bridges registry-shaped data (state.Ports, the orderedEndpointKeys
// scheme map) into the term-package's renderer-shaped data (Endpoint
// records grouped by Category). Kept in this package rather than the
// term package because the endpoint→category mapping is domain
// knowledge (Splice's UI structure), not a generic terminal primitive.
func renderWelcome(out io.Writer, name, spliceVersion string, state *registry.State, elapsed time.Duration) {
	// Group endpoints by purpose. The order here drives display
	// order in the welcome screen — keep the most-clicked surfaces
	// (wallet, scan) at the top.
	endpoints := []term.Endpoint{}
	for _, e := range orderedEndpointKeys() {
		port, ok := state.Ports[e.key]
		if !ok {
			continue
		}
		url := fmt.Sprintf("%s://localhost:%d", e.scheme, port)
		endpoints = append(endpoints, term.Endpoint{
			Category: e.category,
			Label:    e.label,
			URL:      url,
			External: e.external,
		})
	}

	tryNext := []term.NextStep{
		{Label: "Status", Command: fmt.Sprintf("canton-devkit localnet status --name %s", name)},
		{Label: "Env", Command: fmt.Sprintf("eval $(canton-devkit localnet env --name %s)", name)},
		{Label: "Live ACS", Command: fmt.Sprintf("canton-devkit localnet contracts watch --name %s", name)},
		{Label: "Upload DAR", Command: fmt.Sprintf("canton-devkit localnet dar upload <path> --name %s", name)},
		{Label: "Stop", Command: fmt.Sprintf("canton-devkit localnet down --name %s", name)},
	}

	term.WelcomeScreen{
		InstanceName:  name,
		SpliceVersion: spliceVersion,
		Elapsed:       elapsed,
		Endpoints:     endpoints,
		TryNext:       tryNext,
		StatePath:     registry.PathFor(state.Name),
	}.Render(out)
}

// diagnoseUnhealthy inspects the project's containers for the
// known failure patterns we can give targeted remediation for.
// Called from RunUp's wait_healthy fail path.
//
// Returns "" when no recognized pattern matches; caller falls
// back to ErrCodeContainerUnhealthy. Best-effort — if the docker
// probe itself fails (daemon down, project gone), returns "" and
// the wait_healthy failure surfaces with the generic code.
//
// # Why not just the JVM "-Xmx exceeds half" warning
//
// The JVM logs that warning on EVERY startup of a memory-tight
// canton container — it's a JVM config-time advisory, not an OOM
// event. Pattern-matching it would false-positive on every boot
// of a 4 GiB Docker Desktop, AND would false-negative when canton
// actually OOM-kills (the kernel kills the JVM before it can log
// anything; the container restarts and the new JVM's startup log
// has no OOM trace).
//
// True OOM detection signals (any ONE is sufficient):
//
//  1. docker inspect: State.OOMKilled == true on the last
//     terminated state. This is the kernel's own OOM-killer
//     event, not derived from logs.
//  2. State.ExitCode == 137 (SIGKILL) AND State.Status == "exited"
//     — Linux kernel OOM-kill manifests as SIGKILL.
//  3. container is restarting AND its RestartCount is > 0 AND
//     the JVM warning is present in the most recent boot's logs
//     — the warning by itself isn't proof, but COMBINED with an
//     observed restart cycle it strongly suggests OOM-loop.
//
// We use signal (3) here as the cheapest reliable indicator
// (signals 1+2 would need a richer `docker inspect` parse than
// `compose ps` gives us). Wired so the JVM warning alone never
// fires a code — only "warning AND restart-loop" does.
func diagnoseUnhealthy(ctx context.Context, project string) string {
	infos, err := containers.List(ctx, project)
	if err != nil {
		return ""
	}
	for _, c := range infos {
		obs := containerObs{
			Service: c.Service,
			State:   c.State,
			Health:  c.Health,
			Status:  c.Status,
		}
		// Only spend a docker logs subprocess when the cheap
		// signal (Status) already suggests a kernel kill.
		// diagnoseFromObs without LogTail can short-circuit
		// in the common "container is healthy" / "container
		// is just slow-starting" cases.
		if diagnoseFromObs(obs) == "" && !isRestartingOOM(c.Status) {
			continue
		}
		logCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		body, _ := containers.Logs(logCtx, c.Name, containers.LogsOptions{Tail: 80})
		cancel()
		obs.LogTail = body
		if code := diagnoseFromObs(obs); code != "" {
			return code
		}
	}
	return ""
}

// PreflightCodeFromReport (exported variant of the same logic
// used by RunUp internally) extracts the most specific error code
// from a failed docker.Report. Priority order matches
// "what's the most actionable diagnosis the user can fix first":
//
//	Docker CLI missing       → ErrCodeDockerNotInstalled (install Docker)
//	Docker daemon down       → ErrCodeDockerDown        (start Docker)
//	Docker Compose v1/missing → ErrCodeComposeMissing    (install compose v2)
//	Docker memory below floor → ErrCodeMemoryLow        (raise Resources → Memory)
//	Disk space low           → ErrCodeDiskLow           (free space)
//	anything else            → ErrCodePreflightFailed   (catch-all)
//
// Exported so internal/ui/handlers/preflight.go can call it for
// the HTTP-422 response — single source of truth. The lowercase
// wrapper below preserves the local callsite for compatibility
// within this file.
func PreflightCodeFromReport(r *docker.Report) string {
	// Walk in priority order — the first FAIL we hit wins.
	for _, c := range r.Results {
		if c.Status != docker.StatusFail {
			continue
		}
		switch c.Name {
		case "Docker CLI":
			return ErrCodeDockerNotInstalled
		case "Docker daemon":
			return ErrCodeDockerDown
		case "Docker Compose v2":
			return ErrCodeComposeMissing
		case "Docker memory":
			return ErrCodeMemoryLow
		case "Disk space":
			return ErrCodeDiskLow
		}
	}
	return ErrCodePreflightFailed
}

// orderedEndpointKeys returns the endpoint pretty-print order — stable
// regardless of map iteration order. Carries the welcome-screen
// metadata (Category, External) alongside the registry key so the
// renderer can group rows by purpose and render hyperlinks only for
// browsable URLs (not for sockets like postgres).
func orderedEndpointKeys() []endpointDisplay {
	return []endpointDisplay{
		{key: "app_user_ui", label: "Wallet (app-user)", scheme: "http", category: "WEB UIs", external: true},
		{key: "app_provider_ui", label: "Wallet (app-provider)", scheme: "http", category: "WEB UIs", external: true},
		{key: "sv_ui", label: "Wallet (super-validator)", scheme: "http", category: "WEB UIs", external: true},
		{key: "swagger_ui", label: "Swagger (OpenAPI)", scheme: "http", category: "WEB UIs", external: true},
		{key: "postgres", label: "Postgres", scheme: "postgresql", category: "INFRASTRUCTURE", external: false},
	}
}

type endpointDisplay struct {
	key      string
	label    string
	scheme   string
	category string // welcome-screen section header — "WEB UIs", "INFRASTRUCTURE"
	external bool   // browsable URL → render "↗" + OSC 8 hyperlink
}

// shortSHA returns the first 7 characters of a git SHA (or the whole
// string if shorter). Used only for the uncurated-tag warning.
func shortSHA(s string) string {
	if len(s) <= 7 {
		return s
	}
	return s[:7]
}
