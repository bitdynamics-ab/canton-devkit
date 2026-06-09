// Package metricsq is the single source of truth for the PromQL
// queries the CLI's `localnet metrics` and the Web UI's
// `/api/instances/{name}/metrics/summary` both surface.
//
// The two surfaces previously hand-typed
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
// The previous queries here used a `canton_*` prefix
// (e.g. `canton_participant_transactions_total`,
// `canton_mediator_approval_duration_bucket`) that does NOT exist
// in the live Splice Canton's Prometheus. Probing the obs profile
// at LocalNet 0.6.4 surfaced 597 metric names, none with the
// `canton_*` prefix — Splice's OpenTelemetry reporter emits
// `daml_*` (Daml participant / mediator / sequencer), `jvm_*` (heap,
// threads, GC), and `db_client_connections_*` (HikariCP pool stats).
// The `canton_*` strings were aspirational recording-rule names
// that were never actually defined, so every headline returned
// "no data" silently. Queries below are the real, probe-verified
// names; see docs/observability.md for the audit notes and the
// substitute mapping table.
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
	HeadlineMediatorP50  Headline = "mediator_p50_seconds"
	HeadlineMediatorP95  Headline = "mediator_p95_seconds"
	HeadlineMediatorP99  Headline = "mediator_p99_seconds"
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
//
// Query rationale (verified against Splice 0.6.4):
//
//   - LedgerTPS: `daml_participant_api_indexer_updates` is the
//     counter the indexer increments on each ledger update it
//     ingests — closest analog to "transactions per second" the
//     participant sees post-validation. The earlier
//     `canton_participant_transactions_total` does not exist.
//
//   - MediatorP95: there is no `daml_mediator_*` histogram on the
//     mediator approval path. The closest end-to-end protocol
//     latency that IS exposed is
//     `daml_sequencer_client_submissions_sequencing_duration_seconds`,
//     which measures the time from sequencer client send-call until
//     the message is sequenced (i.e. includes mediator approval +
//     ordering). For a developer-overview headline this is the
//     right "how fast is my LocalNet committing things" number.
//
//   - HeapUsed: the OTel JVM reporter uses the label
//     `jvm_memory_type="heap"` (not the old micrometer convention
//     `area="heap"`). Verified on `canton:10013` and `splice:10013`.
//
//   - PostgresConn: no `pg_stat_activity_count` (no postgres
//     exporter is wired into the obs profile). HikariCP, which the
//     Daml participant + Splice apps use to pool DB connections,
//     emits `db_client_connections_usage{state="used"}` per pool;
//     summing it gives the number of DB connections actively held
//     by the JVM processes — functionally the same headline a user
//     would expect from "postgres conns".
var SummaryQueries = map[Headline]string{
	HeadlineLedgerTPS:    "sum(rate(daml_participant_api_indexer_updates[5m]))",
	HeadlineMediatorP50:  "histogram_quantile(0.50, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))",
	HeadlineMediatorP95:  "histogram_quantile(0.95, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))",
	HeadlineMediatorP99:  "histogram_quantile(0.99, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))",
	HeadlineHeapUsed:     `sum(jvm_memory_used_bytes{jvm_memory_type="heap"})`,
	HeadlinePostgresConn: `sum(db_client_connections_usage{state="used"})`,
}

// SchemaVersion is the wire-stable version of the metrics summary
// response. Bumped on a wire-breaking change (rename / removal of
// a Headline). Adding is non-breaking and doesn't require a bump.
//
// The PromQL-string fix to the real metric names did NOT bump this:
// the JSON keys (ledger_tps_5m, mediator_p95_seconds,
// jvm_heap_used_bytes, postgres_conn_count) are unchanged. Only the
// PromQL strings that back them moved.
const SchemaVersion = 1
