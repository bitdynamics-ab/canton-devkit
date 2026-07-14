// Package assets owns the build-time-embedded resources shipped with
// the canton-devkit binary — currently the Prometheus + Grafana
// observability overlay (compose file, prometheus.yml, dashboard JSON,
// provisioning configs).
//
// It lives at the repo root next to `assets/compose/` and
// `assets/grafana/` rather than under `internal/` because Go's
// `//go:embed` directive rejects paths containing `..`: embedded trees
// must live inside (or below) the directory of the .go file declaring
// the directive, so this tiny package's only job is to hold the embed
// and expose the FS.
//
// Consumers read from [FS] using `fs.WalkDir` to materialize files to
// a per-instance destination on disk. See
// internal/localnet/observability_overlay.go for the writer.
package assets

import "embed"

// FS embeds the compose overlays + Grafana provisioning that the
// `localnet up --profile <x>` overlays materialize into an instance's
// data directory at boot. It is NOT observability-specific: every
// profile's compose fragment lives under compose/ in this tree.
//
// Tree shape (post-walk):
//
//	compose/observability.yaml
//	compose/prometheus.yml
//	compose/tokens-v2.yml
//	grafana/dashboards/canton-localnet.json
//	grafana/provisioning/dashboards/canton.yaml
//	grafana/provisioning/datasources/prometheus.yaml
//	nginx/app-provider.conf
//	nginx/app-user.conf
//	nginx/sv.conf
//
//go:embed all:compose all:grafana all:nginx
var FS embed.FS
