// Package metricsq is the single source of truth for the PromQL
// queries the CLI's `localnet metrics` and the Web UI's
// `/api/instances/{name}/metrics/summary` both surface.
//
// BIT-134 review v3 → v4: the two surfaces previously hand-typed
// the same PromQL strings into separate maps — one in
// `internal/cli/localnet/metrics.go`, one in
// `internal/ui/handlers/metrics.go`. A copy-paste error or copy
// drift between the two sides would yield different numbers for
// the same headline on CLI vs Web UI, defeating the AGENTS.md
// "CLI ↔ Web UI parity" rule. Single canonical map here means
// adding a new headline metric (e.g. p99 latency, message-rate
// per sequencer) is a one-line edit that both surfaces pick up
// automatically.
//
// The package deliberately exposes ONLY the typed map + the
// JSON-key string each headline uses on the wire. Rendering /
// transport / unit conversion stays at the call site — those
// concerns differ between CLI text mode and HTTP JSON.
package metricsq

// Headline identifies one of the curated summary panels. The
// frontend's MetricsReport JSON shape and the CLI's text rendering
// both switch on these constants — keep stable.
type Headline string

const (
	HeadlineLedgerTPS    Headline = "ledger_tps_5m"
	HeadlineMediatorP95  Headline = "mediator_p95_seconds"
	HeadlineHeapUsed     Headline = "jvm_heap_used_bytes"
	HeadlinePostgresConn Headline = "postgres_conn_count"
)

// SummaryQueries is the canonical map both `dpm localnet metrics`
// and the `/api/instances/{name}/metrics/summary` handler walk
// when collecting the curated set. Order is irrelevant — both
// surfaces fan out concurrent queries.
//
// Adding an entry here surfaces it on both CLI and Web UI on the
// next rebuild; no further wiring required. Removing or renaming
// an entry is a wire-breaking change to the JSON shape — bump
// the response schema_version when doing it.
var SummaryQueries = map[Headline]string{
	HeadlineLedgerTPS:    "sum(rate(canton_participant_transactions_total[5m]))",
	HeadlineMediatorP95:  "histogram_quantile(0.95, sum(rate(canton_mediator_approval_duration_bucket[5m])) by (le))",
	HeadlineHeapUsed:     `sum(jvm_memory_used_bytes{area="heap"})`,
	HeadlinePostgresConn: "sum(pg_stat_activity_count)",
}

// SchemaVersion is the wire-stable version of the metrics summary
// response. Bumped on a wire-breaking change (rename / removal of
// a Headline). Adding is non-breaking and doesn't require a bump.
const SchemaVersion = 1
