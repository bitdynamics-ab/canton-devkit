package localnet

import (
	"fmt"
	"os"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// ComposeEnv captures everything a `docker compose` invocation against
// an ALREADY-running instance needs to interpolate the Splice base
// compose correctly: the ordered --env-file arguments and (optionally)
// the process environment.
type ComposeEnv struct {
	// EnvFiles are the --env-file paths. Relative paths are resolved
	// against ProjectDir (the ComposeRunner's WorkDir).
	EnvFiles []string
	// Env, when non-nil, replaces the process environment for the
	// compose invocation. When nil, the process inherits normally
	// (which provides PATH, DOCKER_HOST, etc.).
	Env []string
}

// ComposeEnvForInstance returns the compose invocation environment for
// an existing instance.
//
// Preferred path: overlay.env exists on disk (written at `up` time).
// All compose interpolation vars come from env files — Env is nil.
// uiPortOverrides are written to a temporary env file that takes
// highest priority (appended last).
//
// Fallback (pre-existing instances without overlay.env): re-derives
// from the adapter and injects into the process env.
func ComposeEnvForInstance(state *registry.State, uiPortOverrides map[string]int) (ComposeEnv, error) {
	if state == nil {
		return ComposeEnv{}, fmt.Errorf("ComposeEnvForInstance: nil state")
	}

	overlay := overlayEnvPath(state)
	if fileExists(overlay) {
		files := composeEnvFiles(state)
		// If the caller passes port overrides (e.g. observability
		// toggle), write them to a temporary env file that comes
		// after overlay.env so they win.
		if len(uiPortOverrides) > 0 {
			overrideMap := make(map[string]string, len(uiPortOverrides))
			for k, v := range uiPortOverrides {
				overrideMap[k] = fmt.Sprintf("%d", v)
			}
			tmp, err := writeOverrideEnv(state.DataDir, overrideMap)
			if err != nil {
				return ComposeEnv{}, fmt.Errorf("write port overrides: %w", err)
			}
			files = append(files, tmp)
		}
		return ComposeEnv{
			EnvFiles: files,
			Env:      nil, // inherit process env
		}, nil
	}

	// Fallback: re-derive from adapter (backward compat).
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

// writeOverrideEnv writes a small env file for per-invocation overrides
// (e.g. port reallocation during the observability toggle).
func writeOverrideEnv(dataDir string, vars map[string]string) (string, error) {
	path := fmt.Sprintf("%s/override.env", dataDir)
	var b []byte
	for k, v := range vars {
		b = append(b, []byte(k+"="+v+"\n")...)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
