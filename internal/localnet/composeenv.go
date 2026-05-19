package localnet

import (
	"fmt"
	"os"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// composeContext reconstructs the env-vars + --env-file list the
// compose project needs for substitution (PARTY_HINT, LOCALNET_DIR,
// IMAGE_TAG, APP_PROVIDER_AUTH_ENV, …) from the persisted instance
// state. Used by `down` and `logs`, which both invoke `docker compose
// -f compose.yaml ...` and therefore trigger compose's interpolation.
//
// Returns:
//   - env: os.Environ() merged with the adapter's overlay vars
//   - envFiles: relative paths (under state.ProjectDir) of the
//     `--env-file` arguments to pass to docker compose
//   - err: e.g. unknown Splice major in state.SpliceVersion
func composeContext(state *registry.State) (env []string, envFiles []string, err error) {
	v, err := splice.Resolve(state.SpliceVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve version %q from state: %w", state.SpliceVersion, err)
	}
	adapter, err := adapterFor(v)
	if err != nil {
		return nil, nil, err
	}
	overlay := adapter.OverlayEnv(splice.InstanceParams{
		Name:       state.Name,
		Version:    v,
		ProjectDir: state.ProjectDir,
		// Ephemeral and UIPortOverrides aren't needed for `down`/`logs`:
		// compose substitution only requires the variables to be set,
		// not their original values. Empty defaults are fine for
		// teardown / log inspection.
	})

	env = os.Environ()
	for k, v := range overlay {
		env = append(env, k+"="+v)
	}
	return env, adapter.EnvFiles(), nil
}
