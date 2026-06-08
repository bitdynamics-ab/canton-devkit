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
//
// projectDir is the splice-base compose project directory. Docker
// compose resolves relative volume paths in volume mounts (e.g.
// `./prometheus.yml`) against the FIRST compose file's directory,
// not the overlay's. We therefore ALSO write prometheus.yml into
// projectDir so the mount resolves cleanly. Without this, Docker
// auto-creates an empty directory at projectDir/prometheus.yml and
// the bind-mount fails on subsequent ups with "Are you trying to
// mount a directory onto a file?".
func MaterializeObservabilityOverlay(dataDir, projectDir string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("MaterializeObservabilityOverlay: empty dataDir")
	}
	root := filepath.Join(dataDir, "observability")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create observability dir: %w", err)
	}
	// Walk the embedded FS — entries live under "compose/" and
	// "grafana/" at the FS root (see assets/assets.go).
	//
	// Yellow Y5: write-if-different. Previously this clobbered any
	// hand-edited dashboard JSON on every `localnet up`, which is
	// hostile to operators who tweak the Grafana panels for their
	// workload. We now read each destination, hash both sides, and
	// only rewrite when the bytes differ. A real config-management
	// solution (per-instance overrides directory) is tracked in
	// follow-up; this stops the silent stomp without that bigger
	// change.
	if err := fs.WalkDir(assets.FS, ".", func(path string, d fs.DirEntry, err error) error {
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
		data, err := fs.ReadFile(assets.FS, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if existing, err := os.ReadFile(dest); err == nil && bytesEqual(existing, data) {
			return nil
		}
		return os.WriteFile(dest, data, 0o644)
	}); err != nil {
		return "", err
	}
	composeFile := filepath.Join(root, "compose", "observability.yaml")
	if _, err := os.Stat(composeFile); err != nil {
		return "", fmt.Errorf("observability overlay missing after extract: %w", err)
	}

	// Drop prometheus.yml into projectDir so Docker's relative
	// volume-mount resolution finds it. The overlay's bind mount
	// declares `./prometheus.yml` which docker compose resolves
	// against the FIRST -f file's directory (the splice base, ie
	// projectDir), NOT the overlay's own directory. If the file
	// isn't there, Docker auto-creates an empty directory at that
	// path on first try and every subsequent up fails with "mount
	// directory onto file".
	if projectDir != "" {
		promSrc, err := fs.ReadFile(assets.FS, "compose/prometheus.yml")
		if err != nil {
			return "", fmt.Errorf("read embedded prometheus.yml: %w", err)
		}
		promDst := filepath.Join(projectDir, "prometheus.yml")
		// Defensive cleanup: if a prior failed run left an empty
		// directory in projectDir, Docker can't replace it with a
		// file via WriteFile alone. Remove first if it's a dir.
		if st, err := os.Stat(promDst); err == nil && st.IsDir() {
			if err := os.RemoveAll(promDst); err != nil {
				return "", fmt.Errorf("clear stale prometheus.yml directory: %w", err)
			}
		}
		// Yellow Y5: only rewrite when bytes differ — don't clobber
		// hand edits silently.
		if existing, err := os.ReadFile(promDst); err == nil && bytesEqual(existing, promSrc) {
			// no-op
		} else if err := os.WriteFile(promDst, promSrc, 0o644); err != nil {
			return "", fmt.Errorf("write prometheus.yml to project dir: %w", err)
		}
	}

	return composeFile, nil
}

// bytesEqual is a tiny shim so we don't have to import bytes for
// one call. Equivalent to bytes.Equal but lets us keep the import
// list focused on filesystem APIs.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ObservabilityProfileName is the legacy umbrella docker compose
// profile that activates BOTH Prometheus and Grafana together. Kept
// for backward compatibility (existing scripts, docs, registry
// entries). Prefer the per-component profiles PrometheusProfileName
// / GrafanaProfileName for new code so users can enable each side
// independently.
const ObservabilityProfileName = "observability"

// PrometheusProfileName activates only the Prometheus service in the
// observability overlay. Use alongside GrafanaProfileName to mirror
// the legacy umbrella, or alone for metrics-scrape-only setups.
const PrometheusProfileName = "prometheus"

// GrafanaProfileName activates only the Grafana service. Pointed at
// the bundled Prometheus by default; in a Grafana-only setup the
// user is expected to wire it to an external scrape source — the
// API + UI surface a warning when grafana is enabled without
// prometheus.
const GrafanaProfileName = "grafana"

// ExpandObservabilityProfiles returns the de-duplicated set of
// per-component profiles equivalent to the input list. Legacy
// "observability" expands to ["prometheus", "grafana"]; the
// per-component names pass through. Used by both the CLI bring-up
// path and the HTTP toggle so a single source-of-truth governs the
// "which sidecars start?" decision.
func ExpandObservabilityProfiles(profiles []string) (prometheus, grafana bool) {
	for _, p := range profiles {
		switch p {
		case ObservabilityProfileName:
			prometheus = true
			grafana = true
		case PrometheusProfileName:
			prometheus = true
		case GrafanaProfileName:
			grafana = true
		}
	}
	return
}
