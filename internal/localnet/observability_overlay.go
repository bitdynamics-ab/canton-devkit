package localnet

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bitdynamics-ab/canton-devkit/assets"
)

// MaterializeObservabilityOverlay writes the embedded compose +
// grafana files (from the `assets` package's go:embed FS) into a
// per-instance directory and returns the absolute path of the
// compose YAML to append to ComposeRunner.ComposeFiles.
//
// Idempotent: invocations after the first reuse the existing dir.
// Caller is responsible for cleanup tied to RunDown.
//
// dataDir is the per-instance data directory (registry's DataDir)
// — overlay lands under `<dataDir>/observability/` so per-instance
// overrides (e.g. custom dashboard JSONs) can be slotted in
// alongside the embedded baseline.
func MaterializeObservabilityOverlay(dataDir string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("MaterializeObservabilityOverlay: empty dataDir")
	}
	root := filepath.Join(dataDir, "observability")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create observability dir: %w", err)
	}
	// Walk the embedded FS — entries live under "compose/" and
	// "grafana/" at the FS root (see assets/assets.go).
	if err := fs.WalkDir(assets.Observability, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		dest := filepath.Join(root, path)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(assets.Observability, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	}); err != nil {
		return "", err
	}
	composeFile := filepath.Join(root, "compose", "observability.yaml")
	if _, err := os.Stat(composeFile); err != nil {
		return "", fmt.Errorf("observability overlay missing after extract: %w", err)
	}
	return composeFile, nil
}

// ObservabilityProfileName is the docker compose profile name
// scoping the Prometheus + Grafana services. The compose overlay
// declares `profiles: ["observability"]` on each service so they
// stay off unless this profile is activated.
const ObservabilityProfileName = "observability"
