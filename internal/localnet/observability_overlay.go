package localnet

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
//
// warnw receives a one-line notice for every materialized file an
// operator has edited away from the embedded baseline; it may be nil.
func MaterializeObservabilityOverlay(dataDir, projectDir string, warnw io.Writer) (string, error) {
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
	// Edit-preserving write. Operators who tweak a Grafana dashboard or
	// the prometheus scrape config for their workload must keep those
	// edits across a `localnet up`. We write a destination only when it
	// is missing or still byte-identical to the embedded baseline; an
	// edited file is left in place and reported on warnw. (The compose
	// overlay itself is the one exception — see below — because its
	// bind-mount paths are rewritten to per-instance absolutes and must
	// track this dataDir.)
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
		// The compose overlay is rewritten in place by
		// rewriteObservabilityMounts below to point at per-instance
		// absolute mount paths, so it can never match the embedded
		// bytes and must always be refreshed from the baseline. All
		// other files (dashboards, scrape config) are edit-preserving.
		if filepath.ToSlash(path) == "compose/observability.yaml" {
			return os.WriteFile(dest, data, 0o644)
		}
		return writePreservingEdits(dest, data, warnw)
	}); err != nil {
		return "", err
	}
	composeFile := filepath.Join(root, "compose", "observability.yaml")
	if _, err := os.Stat(composeFile); err != nil {
		return "", fmt.Errorf("observability overlay missing after extract: %w", err)
	}
	if err := rewriteObservabilityMounts(composeFile, root); err != nil {
		return "", err
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
		// Edit-preserving: leave a hand-edited project-dir copy alone
		// (warn on drift), only write when missing or unchanged.
		if err := writePreservingEdits(promDst, promSrc, warnw); err != nil {
			return "", fmt.Errorf("write prometheus.yml to project dir: %w", err)
		}
	}

	return composeFile, nil
}

// writePreservingEdits writes data to dest only when dest is missing or
// still byte-identical to data (the embedded baseline). When dest
// already exists with different bytes — i.e. an operator has edited it
// — it is left untouched and a one-line drift notice is written to
// warnw (when non-nil). This is the shared primitive behind the
// edit-preserving contract of both materialised overlays: an operator
// who tweaks an overlay in place keeps that tweak across `localnet up`.
//
// A read error other than not-exist is treated as "write the baseline"
// rather than failing the whole bring-up over an unreadable stale file.
func writePreservingEdits(dest string, data []byte, warnw io.Writer) error {
	existing, rerr := os.ReadFile(dest)
	switch {
	case rerr == nil && bytesEqual(existing, data):
		// Already the canonical content — nothing to do.
		return nil
	case rerr == nil:
		// Operator-edited: preserve it, but make the divergence visible.
		if warnw != nil {
			_, _ = fmt.Fprintf(warnw,
				"preserving local edits to %s (differs from the bundled default; "+
					"delete it to restore the default on the next `localnet up`)\n",
				dest)
		}
		return nil
	default:
		// Missing (or unreadable) — install the baseline.
		return os.WriteFile(dest, data, 0o644)
	}
}

func rewriteObservabilityMounts(composeFile, root string) error {
	body, err := os.ReadFile(composeFile)
	if err != nil {
		return fmt.Errorf("read observability overlay: %w", err)
	}
	s := string(body)
	replacements := map[string]string{
		"./prometheus.yml":        filepath.Join(root, "compose", "prometheus.yml"),
		"../grafana/provisioning": filepath.Join(root, "grafana", "provisioning"),
		"../grafana/dashboards":   filepath.Join(root, "grafana", "dashboards"),
	}
	for from, to := range replacements {
		s = strings.ReplaceAll(s, from, filepath.ToSlash(to))
	}
	if string(body) == s {
		return nil
	}
	if err := os.WriteFile(composeFile, []byte(s), 0o644); err != nil {
		return fmt.Errorf("rewrite observability overlay mounts: %w", err)
	}
	return nil
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
