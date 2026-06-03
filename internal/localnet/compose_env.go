package localnet

import (
	"fmt"
	"os"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// ComposeEnv captures everything a `docker compose` invocation against
// an ALREADY-running instance needs to interpolate the Splice base
// compose correctly: the ordered --env-file arguments (relative to the
// instance's ProjectDir) and the full process environment (the
// inherited os.Environ() plus the adapter's computed OverlayEnv).
type ComposeEnv struct {
	// EnvFiles are the --env-file paths, relative to the instance
	// ProjectDir. Pass each as a `--env-file <f>` arg and run with
	// the command's working directory set to ProjectDir.
	EnvFiles []string
	// Env is the complete process environment, ready to assign to
	// exec.Cmd.Env (already includes os.Environ()).
	Env []string
}

// ComposeEnvForInstance reconstructs the compose invocation
// environment for an existing instance from its persisted registry
// state. RunUp builds this fresh from the splice adapter on every
// bring-up; callers that run `docker compose` against an instance
// OUTSIDE of RunUp — notably the Web UI's runtime observability
// toggle (enableObservability) — MUST use this helper.
//
// Without the adapter's OverlayEnv (PARTY_HINT, LOCALNET_DIR,
// LOCALNET_ENV_DIR, IMAGE_TAG, DOCKER_NETWORK, COMPOSE_PROFILES,
// ALPHA_PROTOCOL_VERSION_ENV, TEST_PORT) and EnvFiles, docker compose
// fails to interpolate the Splice base compose and aborts with e.g.
// "required variable PARTY_HINT is missing a value" — before any
// target service (prometheus/grafana) can start.
//
// uiPortOverrides maps the port env-var names the compose files
// substitute (APP_USER_UI_PORT, APP_PROVIDER_UI_PORT, SV_UI_PORT,
// SWAGGER_UI_PORT, DB_PORT, and optionally PROMETHEUS_HOST_PORT /
// GRAFANA_HOST_PORT) to the host ports the instance already publishes,
// so re-running compose reuses them instead of reassigning. Derive the
// values from state.Ports.
func ComposeEnvForInstance(state *registry.State, uiPortOverrides map[string]int) (ComposeEnv, error) {
	if state == nil {
		return ComposeEnv{}, fmt.Errorf("ComposeEnvForInstance: nil state")
	}
	v, err := splice.Resolve(state.SpliceVersion)
	if err != nil {
		return ComposeEnv{}, fmt.Errorf("resolve splice version %q: %w", state.SpliceVersion, err)
	}
	adapter, err := adapterFor(v)
	if err != nil {
		return ComposeEnv{}, fmt.Errorf("adapter for %q: %w", state.SpliceVersion, err)
	}
	params := splice.InstanceParams{
		Name:            state.Name,
		Version:         v,
		ProjectDir:      state.ProjectDir,
		Ephemeral:       true,
		UIPortOverrides: uiPortOverrides,
	}
	return ComposeEnv{
		EnvFiles: adapter.EnvFiles(),
		Env:      append(os.Environ(), mapToEnv(adapter.OverlayEnv(params))...),
	}, nil
}
