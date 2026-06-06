package localnet

import (
	"fmt"
	"os"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
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
// an existing instance. All compose interpolation vars come from the
// overlay.env file written at `up` time — Env is always nil.
//
// uiPortOverrides (e.g. observability toggle) are written to a
// temporary override.env file appended after overlay.env so they win.
func ComposeEnvForInstance(state *registry.State, uiPortOverrides map[string]int) (ComposeEnv, error) {
	if state == nil {
		return ComposeEnv{}, fmt.Errorf("ComposeEnvForInstance: nil state")
	}

	files, err := composeEnvFiles(state)
	if err != nil {
		return ComposeEnv{}, err
	}

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
		Env:      nil,
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
