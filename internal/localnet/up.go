package localnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"slices"
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

type ExitCodeError int

func (e ExitCodeError) Error() string { return fmt.Sprintf("exit code %d", int(e)) }

func AsExitError(code int) error {
	if code == 0 {
		return nil
	}
	return ExitCodeError(code)
}

type composeOps interface {
	Up(ctx context.Context) error
	WaitForHealthy(ctx context.Context) error
}

type UpOptions struct {
	Name    string
	Version string

	AllowUncurated bool

	Profiles []string

	ObservabilityMode ObservabilityMode

	PortBase int

	SkipPreflight bool

	FetchFn   func(ctx context.Context, v splice.Version, cacheRoot string, out io.Writer) (string, error)
	NewRunner func(projectName string, composeFiles, envFiles, env []string, workDir string, logw io.Writer) composeOps
}

func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if err := registry.ValidateName(name); err != nil {
		return fmt.Errorf("--name %w", err)
	}
	return nil
}

func RunUp(ctx context.Context, prog Progress, opts *UpOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := ValidateProfiles(opts.Profiles); err != nil {
		prog.FailStep(StepResolveVersion, err.Error(), nil)
		return ExitUserError
	}

	startedAt := time.Now()

	prog.StartStep(StepResolveVersion, "")
	version, fromUpstream, err := splice.ResolveOrUpstream(ctx, opts.Version, opts.AllowUncurated)
	if err != nil {
		prog.FailStep(StepResolveVersion, err.Error(), nil)
		return ExitUserError
	}
	if fromUpstream {
		prog.Warn(fmt.Sprintf(
			"using uncurated Splice tag %q (resolved upstream to commit %s); not tested by DevKit",
			version.Tag, shortSHA(version.Commit)))
	}
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

	_, _ = fmt.Fprintf(prog.Out(), "Starting Canton LocalNet %q (Splice %s, adapter %s)...\n",
		opts.Name, version.Tag, adapter.MajorVersion())

	prog.StartStep(StepAcquireLock, "")
	release, err := registry.Lock(opts.Name)
	if err != nil {
		prog.FailStep(StepAcquireLock, err.Error(), nil)
		return ExitUserError
	}
	defer release()
	prog.FinishStep(StepAcquireLock, "")

	if prior, rerr := registry.Read(opts.Name); rerr == nil {
		if len(opts.Profiles) == 0 {
			if optIns := subtractProfiles(prior.Profiles, adapter.Profiles()); len(optIns) > 0 {
				opts.Profiles = optIns
				prog.Warn(fmt.Sprintf(
					"re-enabling profiles from the previous up of %q: %s "+
						"(pass --profile explicitly to change the set)",
					opts.Name, strings.Join(optIns, ", ")))
			}
		}
		if opts.ObservabilityMode == "" && prior.ObservabilityMode != "" {
			opts.ObservabilityMode = ObservabilityMode(prior.ObservabilityMode)
		}
	}

	if !opts.SkipPreflight {
		prog.StartStep(StepPreflight, "")
		report := docker.RunPreflight(ctx, docker.Options{
			DataDir:                registry.Root(),
			MinDiskBytes:           docker.DefaultMinDiskBytes,
			MinMemoryBytes:         splice.MinMemoryFor(version),
			RecommendedMemoryBytes: splice.RecommendedMemoryFor(version),
		})
		telemetry.Inc("dpm/docker_engine", telemetry.NormEngine(report.DockerEngine))
		telemetry.Inc("dpm/compose_version_bucket", telemetry.ComposeBucket(report.ComposeVersion))
		report.Write(prog.Out())
		if !report.OK() {
			prog.FailStep(StepPreflight,
				"\nPreflight failed. Address the items above and re-run.",
				WithCode(fmt.Errorf("preflight failed"), PreflightCodeFromReport(report)))
			return ExitPreflightFail
		}
		prog.FinishStep(StepPreflight, "")
	}

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

	prog.StartStep(StepPersistState, "")
	dataDir := registry.DataDirFor(opts.Name)
	containerPrefix := opts.Name + "-"
	overlayPath, err := WriteContainerRenameOverlay(dataDir, containerPrefix)
	if err != nil {
		prog.FailStep(StepPersistState, "Failed to write container-rename overlay", err)
		return ExitRuntimeFailure
	}

	composeFiles := append([]string(nil), adapter.ComposeFiles()...)
	composeFiles = append(composeFiles, overlayPath)

	loopbackPath, err := WriteLoopbackPortsOverlay(dataDir)
	if err != nil {
		prog.FailStep(StepPersistState, "Failed to write loopback-ports overlay", err)
		return ExitRuntimeFailure
	}
	composeFiles = append(composeFiles, loopbackPath)

	nginxPath, err := WriteNginxVhostOverlay(dataDir, opts.Name, prog.Err())
	if err != nil {
		prog.FailStep(StepPersistState, "Failed to write nginx-vhost overlay", err)
		return ExitRuntimeFailure
	}
	composeFiles = append(composeFiles, nginxPath)

	wantPrometheus, wantGrafana := ExpandObservabilityProfiles(opts.Profiles)
	hasObservabilityProfile := wantPrometheus || wantGrafana
	if wantGrafana && !wantPrometheus {
		prog.Warn("--profile grafana enabled without --profile prometheus: " +
			"Grafana will start with no bundled data source. Add " +
			"--profile prometheus, or configure an external Prometheus " +
			"manually before relying on the dashboards.")
	}
	perInstanceOverlay := false
	if hasObservabilityProfile {
		mode := normalizeObservabilityMode(opts.ObservabilityMode)
		sharedReachable := false
		if mode != ObservabilityModePerInstance {
			sharedReachable = sharedStackReachable(ctx, prog.Err())
			if mode == ObservabilityModeShared && !sharedReachable {
				prog.Warn("--observability-mode shared: the host-shared " +
					"observability stack is not reachable; metrics stay " +
					"unavailable until it is.")
			}
		}
		perInstanceOverlay = usePerInstanceOverlay(mode, sharedReachable)
		if perInstanceOverlay {
			overlay, err := MaterializeObservabilityOverlay(dataDir, projectDir, prog.Err())
			if err != nil {
				prog.FailStep(StepPersistState,
					"Failed to extract observability overlay", err)
				return ExitRuntimeFailure
			}
			composeFiles = append(composeFiles, overlay)
		}
	}

	hasTokensV2Profile := slices.Contains(opts.Profiles, TokensV2ProfileName)
	if hasTokensV2Profile {
		overlay, err := MaterializeTokensV2Overlay(dataDir, prog.Err())
		if err != nil {
			prog.FailStep(StepPersistState,
				"Failed to extract tokens-v2 overlay", err)
			return ExitRuntimeFailure
		}
		composeFiles = append(composeFiles, overlay)
	} else if version.IsAlpha() {
		prog.Warn(fmt.Sprintf(
			"alpha Splice %q selected without --profile %s — Canton "+
				"will not enable initial-protocol-version=35; V2 features "+
				"WILL fail. Re-run with --profile %s to inject the alpha-protocol config.",
			version.Tag, TokensV2ProfileName, TokensV2ProfileName))
	}

	var priorPorts map[string]int
	var priorImageDigests map[string]string
	if prior, err := registry.Read(opts.Name); err == nil {
		if prior.Status == registry.StatusRunning {
			prog.FailStep(StepPersistState, fmt.Sprintf(
				"instance %q is already running — stop it first with "+
					"`localnet down %s`, or bounce it with `localnet restart %s`",
				opts.Name, opts.Name, opts.Name), nil)
			return ExitUserError
		}
		priorPorts = prior.Ports
		if prior.SpliceVersion == version.Tag {
			priorImageDigests = prior.ImageDigests
		}
	}

	enabledProfiles := append(adapter.Profiles(), opts.Profiles...)

	state := registry.NewState(opts.Name, version.Tag)
	state.ComposeProject = "canton-" + opts.Name
	state.ComposeFiles = composeFiles
	state.Profiles = enabledProfiles
	if hasObservabilityProfile {
		state.ObservabilityMode = string(normalizeObservabilityMode(opts.ObservabilityMode))
	}
	state.DockerNetwork = opts.Name
	state.ContainerPrefix = containerPrefix
	state.ProjectDir = projectDir
	state.DataDir = dataDir
	state.Ports = adapter.EndpointMap()
	state.AlphaProtocolEnabled = adapter.SupportsAlphaProtocol()
	state.Status = registry.StatusCreating
	if err := registry.Write(state); err != nil {
		prog.FailStep(StepPersistState, "Failed to write registry state", err)
		return ExitRuntimeFailure
	}

	// Pre-allocate UI host ports. Splice's nginx/swagger/postgres
	// services don't honor TEST_PORT — they only consume the per-service
	// env vars (APP_USER_UI_PORT etc).
	portEnvVars := UIPortEnvVars()
	if wantPrometheus {
		portEnvVars = append(portEnvVars, "PROMETHEUS_HOST_PORT")
	}
	if wantGrafana {
		portEnvVars = append(portEnvVars, "GRAFANA_HOST_PORT")
	}
	var uiOverrides map[string]int
	if opts.PortBase > 0 {
		uiOverrides, err = DeriveUIPorts(portEnvVars, opts.PortBase)
	} else {
		uiOverrides, err = ReuseOrAllocateUIPorts(portEnvVars, priorPorts)
	}
	if err != nil {
		var conflict *PortBaseConflict
		if errors.Is(err, ErrPortBusy) || errors.As(err, &conflict) {
			prog.FailStep(StepPersistState,
				fmt.Sprintf("%s\nFree the conflicting process (lsof -i :<port>), pick a different --port-base, or tear down the "+
					"other instance and re-run `localnet up %s`.", err, opts.Name),
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
	if wantPrometheus && perInstanceOverlay {
		state.Ports["prometheus_ui"] = uiOverrides["PROMETHEUS_HOST_PORT"]
	}
	if wantGrafana && perInstanceOverlay {
		state.Ports["grafana_ui"] = uiOverrides["GRAFANA_HOST_PORT"]
	}
	prog.FinishStep(StepPersistState, "")

	params := splice.InstanceParams{
		Name:            opts.Name,
		Version:         version,
		ProjectDir:      projectDir,
		Ephemeral:       true,
		UIPortOverrides: uiOverrides,
	}
	overlayVars := adapter.OverlayEnv(params)

	overlayEnvPath, err := WriteOverlayEnv(dataDir, overlayVars)
	if err != nil {
		prog.FailStep(StepPersistState, "Failed to write overlay env", err)
		return ExitRuntimeFailure
	}

	envFiles := append(adapter.EnvFiles(), overlayEnvPath)

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

	prog.StartStep(StepWaitHealthy, "")
	if err := runner.WaitForHealthy(ctx); err != nil {
		markFailed(state, prog.Err())
		if ctx.Err() != nil {
			prog.FailStep(StepWaitHealthy, "Timed out waiting for services", nil)
			return ExitTimeout
		}
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

	for key, port := range CaptureCantonPorts(ctx, state.ComposeProject) {
		state.Ports[key] = port
	}
	for key, port := range CaptureMetricsPorts(ctx, state.ComposeProject) {
		state.Ports[key] = port
	}

	if digests := CaptureImageDigests(ctx, state.ComposeProject); len(digests) > 0 {
		if changed := DiffImageDigests(priorImageDigests, digests); len(changed) > 0 {
			prog.Warn(fmt.Sprintf(
				"Splice image digest(s) changed since the last up of %q — a mutable ghcr tag was republished; "+
					"verify this is expected: %s",
				version.Tag, strings.Join(changed, ", ")))
		}
		state.ImageDigests = digests
	}

	prog.StartStep(StepCaptureJWTs, "")
	if creds := captureCredentials(projectDir, prog.Err()); creds != nil {
		state.Credentials = creds
	}
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		prog.Warn(fmt.Sprintf("services healthy but registry write failed: %s", err))
	}
	prog.FinishStep(StepCaptureJWTs, "")

	if hasObservabilityProfile {
		if _, err := RegisterInstanceAndEnsureStack(ctx, state, prog.Err()); err != nil {
			prog.Warn("shared observability: " + err.Error())
		}
	}

	renderWelcome(prog.Out(), opts.Name, version.Tag, state, time.Since(startedAt))

	prog.Done("")
	return ExitSuccess
}

func captureCredentials(projectDir string, errw io.Writer) map[string]registry.Credential {
	inputs, err := splice.LoadCredentialInputs(projectDir)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not read auth env files: %s\n", err)
		return nil
	}
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

func renderWelcome(out io.Writer, name, spliceVersion string, state *registry.State, elapsed time.Duration) {
	endpoints := []term.Endpoint{}
	for _, e := range orderedEndpointKeys() {
		port, ok := state.Ports[e.key]
		if !ok {
			continue
		}
		host := "localhost"
		if vh := walletVHostForKey(e.key, name); vh != "" {
			host = vh
		}
		url := fmt.Sprintf("%s://%s:%d", e.scheme, host, port)
		endpoints = append(endpoints, term.Endpoint{
			Category: e.category,
			Label:    e.label,
			URL:      url,
			External: e.external,
		})
	}

	tryNext := []term.NextStep{
		{Label: "Status", Command: fmt.Sprintf("canton-devkit localnet status %s", name)},
		{Label: "Env", Command: fmt.Sprintf("eval $(canton-devkit localnet env %s)", name)},
		{Label: "Live ACS", Command: fmt.Sprintf("canton-devkit localnet contracts watch --name %s", name)},
		{Label: "Upload DAR", Command: fmt.Sprintf("canton-devkit localnet dar upload <path> --name %s", name)},
		{Label: "Stop", Command: fmt.Sprintf("canton-devkit localnet down %s", name)},
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

// The JVM logs that warning on EVERY startup of a memory-tight
// canton container — it's a JVM config-time advisory, not an OOM
// event. Pattern-matching it would false-positive on every boot
// of a 4 GiB Docker Desktop, AND would false-negative when canton
// actually OOM-kills (the kernel kills the JVM before it can log
// anything; the container restarts and the new JVM's startup log
// has no OOM trace).
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

func PreflightCodeFromReport(r *docker.Report) string {
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

func orderedEndpointKeys() []endpointDisplay {
	return []endpointDisplay{
		{key: "app_user_ui", label: "Wallet (username: app-user)", scheme: "http", category: "WEB UIs", external: true},
		{key: "app_provider_ui", label: "Wallet (username: app-provider)", scheme: "http", category: "WEB UIs", external: true},
		{key: "sv_ui", label: "Wallet (username: super-validator)", scheme: "http", category: "WEB UIs", external: true},
		{key: "swagger_ui", label: "Swagger (OpenAPI)", scheme: "http", category: "WEB UIs", external: true},
		{key: "postgres", label: "Postgres", scheme: "postgresql", category: "INFRASTRUCTURE", external: false},
	}
}

type endpointDisplay struct {
	key      string
	label    string
	scheme   string
	category string
	external bool
}

var walletUIRoleByKey = map[string]string{
	"app_user_ui":     "app-user",
	"app_provider_ui": "app-provider",
	"sv_ui":           "sv",
}

func walletVHostForKey(key, instance string) string {
	role, ok := walletUIRoleByKey[key]
	if !ok {
		return ""
	}
	return instanceVHostRole(VHostServiceWallet, role, instance)
}

func shortSHA(s string) string {
	if len(s) <= 7 {
		return s
	}
	return s[:7]
}
