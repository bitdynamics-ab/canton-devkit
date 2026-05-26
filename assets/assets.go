// Package assets owns the build-time-embedded resources shipped with
// the canton-devkit binary — currently the BIT-134 Prometheus + Grafana
// observability overlay (compose file, prometheus.yml, dashboard JSON,
// provisioning configs).
//
// Why this package lives at the repo root next to `assets/compose/`
// and `assets/grafana/` rather than under `internal/`:
//
//   - Go's `//go:embed` directive rejects paths containing `..` — embedded
//     trees must live inside (or below) the directory of the .go file
//     declaring the directive. The previous home for this embed was
//     `internal/localnet/observability_overlay.go` with
//     `//go:embed all:../../assets/compose` — that does not compile
//     ("invalid pattern syntax").
//   - Co-locating the .go file with the asset trees is the conventional
//     workaround: one tiny package whose only job is to hold the embed
//     and expose the FS to anyone who needs it.
//
// Consumers import this package and read from [Observability], using
// `fs.WalkDir` to materialize files to a per-instance destination on
// disk. See internal/localnet/observability_overlay.go for the writer.
package assets

import "embed"

// Observability embeds the compose overlay + Grafana provisioning that
// `canton-devkit localnet up --profile observability` materializes
// into the instance's data directory at boot time.
//
// Tree shape (post-walk):
//
//	compose/observability.yaml
//	compose/prometheus.yml
//	grafana/dashboards/canton-localnet.json
//	grafana/provisioning/dashboards/canton.yaml
//	grafana/provisioning/datasources/prometheus.yaml
//
//go:embed all:compose all:grafana
var Observability embed.FS
