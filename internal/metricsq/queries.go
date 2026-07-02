// Package metricsq is the single source of truth for the PromQL
// queries the CLI's `localnet metrics` and the Web UI's
// `/api/instances/{name}/metrics/summary` both surface. A single
// canonical map keeps the two surfaces in parity: adding a headline
// metric is a one-line edit both pick up automatically, and the same
// PromQL can never drift between them.
//
// Metric names are probe-verified against a live Splice LocalNet
// (0.6.4 obs profile): Splice's OpenTelemetry reporter emits `daml_*`
// (participant / mediator / sequencer), `jvm_*` (heap, threads, GC),
// and `db_client_connections_*` (HikariCP pool stats) — there is no
// `canton_*` prefix, and an unverified name fails silently as
// "no data". See docs/observability.md for the audit notes and the
// substitute mapping table.
//
// The package deliberately exposes ONLY the typed map + the
// JSON-key string each headline uses on the wire. Rendering /
// transport / unit conversion stays at the call site — those
// concerns differ between CLI text mode and HTTP JSON.
package metricsq

import "strings"

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
//     participant sees post-validation.
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
//
// SummaryQueries is the unscoped set (sums across every series Prometheus
// holds). Correct for a per-instance Prometheus that only scrapes one
// LocalNet. For the SHARED host-level stack, which scrapes every
// instance, use SummaryQueriesFor(<instance>) so a headline reflects one
// instance, not the sum across all of them.
var SummaryQueries = SummaryQueriesFor("")

// SummaryQueriesFor builds the curated PromQL set, optionally scoped to a
// single instance. When instance is non-empty an `instance="<inst>"`
// label matcher is injected into every metric selector — this matches the
// `instance` label the shared stack's file_sd attaches to each target. An
// empty instance reproduces the unscoped queries byte-for-byte (so the
// per-instance Prometheus path is unchanged).
//
// Building the selectors here (rather than hand-writing two copies) keeps
// the CLI and Web UI byte-identical, and composes the label matcher
// correctly whether or not the metric already carries its own labels (no
// invalid trailing comma).
func SummaryQueriesFor(instance string) map[Headline]string {
	sel := func(metric, extra string) string {
		var matchers []string
		if instance != "" {
			matchers = append(matchers, `instance="`+instance+`"`)
		}
		if extra != "" {
			matchers = append(matchers, extra)
		}
		if len(matchers) == 0 {
			return metric
		}
		return metric + "{" + strings.Join(matchers, ",") + "}"
	}
	const bucket = "daml_sequencer_client_submissions_sequencing_duration_seconds_bucket"
	return map[Headline]string{
		HeadlineLedgerTPS:    "sum(rate(" + sel("daml_participant_api_indexer_updates", "") + "[5m])) or vector(0)",
		HeadlineMediatorP50:  "histogram_quantile(0.50, sum(rate(" + sel(bucket, "") + "[5m])) by (le))",
		HeadlineMediatorP95:  "histogram_quantile(0.95, sum(rate(" + sel(bucket, "") + "[5m])) by (le))",
		HeadlineMediatorP99:  "histogram_quantile(0.99, sum(rate(" + sel(bucket, "") + "[5m])) by (le))",
		HeadlineHeapUsed:     "sum(" + sel("jvm_memory_used_bytes", `jvm_memory_type="heap"`) + ")",
		HeadlinePostgresConn: "sum(" + sel("db_client_connections_usage", `state="used"`) + ")",
	}
}

// SchemaVersion is the wire-stable version of the metrics summary
// response. Bumped on a wire-breaking change (rename / removal of
// a Headline). Adding is non-breaking and doesn't require a bump;
// neither does changing the PromQL string behind an unchanged
// JSON key.
const SchemaVersion = 1
