// Package metricsq is the single source of the PromQL queries the CLI's
// `localnet metrics` and the Web UI's metrics summary both surface.
//
// Metric names are probe-verified against Splice 0.6.4: it emits `daml_*`,
// `jvm_*`, and `db_client_connections_*` (no `canton_*` prefix), and an
// unverified name fails silently as "no data". See docs/observability.md.
package metricsq

import "strings"

// Headline identifies one curated summary panel. Both surfaces switch on
// these constants — keep stable.
type Headline string

const (
	HeadlineLedgerTPS    Headline = "ledger_tps_5m"
	HeadlineMediatorAvg  Headline = "mediator_avg_seconds"
	HeadlineMediatorP50  Headline = "mediator_p50_seconds"
	HeadlineMediatorP95  Headline = "mediator_p95_seconds"
	HeadlineMediatorP99  Headline = "mediator_p99_seconds"
	HeadlineHeapUsed     Headline = "jvm_heap_used_bytes"
	HeadlinePostgresConn Headline = "postgres_conn_count"
)

// SummaryQueries is the unscoped curated set (sums across every series).
// Removing/renaming an entry breaks the JSON shape — bump SchemaVersion.
//
// Substitute-metric notes (verified against Splice 0.6.4):
//   - LedgerTPS uses daml_participant_api_indexer_updates, the closest
//     analog to committed TPS the participant exposes.
//   - MediatorP*/Avg: no daml_mediator_* histogram exists; the sequencer
//     submission-duration histogram (includes mediator approval + ordering)
//     is the substitute. 0.6.4 exports it with only the +Inf bucket, so
//     histogram_quantile is NaN and the mean (_sum/_count) is the reliable
//     headline; percentiles show only when finite `le` buckets are present.
//   - HeapUsed: OTel labels heap `jvm_memory_type="heap"` (not area="heap").
//   - PostgresConn: no postgres exporter; sum HikariCP's
//     db_client_connections_usage{state="used"} as the substitute.
//
// For the SHARED multi-instance stack use SummaryQueriesFor(<instance>) so a
// headline reflects one instance, not the sum across all of them.
var SummaryQueries = SummaryQueriesFor("")

// SummaryQueriesFor builds the curated PromQL set, optionally scoped to a
// single instance. A non-empty instance injects an `instance="<inst>"`
// matcher into every selector (matching the shared stack's file_sd label);
// empty reproduces the unscoped queries byte-for-byte.
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
	const (
		bucket = "daml_sequencer_client_submissions_sequencing_duration_seconds_bucket"
		sumM   = "daml_sequencer_client_submissions_sequencing_duration_seconds_sum"
		countM = "daml_sequencer_client_submissions_sequencing_duration_seconds_count"
	)
	return map[Headline]string{
		HeadlineLedgerTPS:    "sum(rate(" + sel("daml_participant_api_indexer_updates", "") + "[5m])) or vector(0)",
		HeadlineMediatorAvg:  "sum(rate(" + sel(sumM, "") + "[5m])) / sum(rate(" + sel(countM, "") + "[5m]))",
		HeadlineMediatorP50:  "histogram_quantile(0.50, sum(rate(" + sel(bucket, "") + "[5m])) by (le))",
		HeadlineMediatorP95:  "histogram_quantile(0.95, sum(rate(" + sel(bucket, "") + "[5m])) by (le))",
		HeadlineMediatorP99:  "histogram_quantile(0.99, sum(rate(" + sel(bucket, "") + "[5m])) by (le))",
		HeadlineHeapUsed:     "sum(" + sel("jvm_memory_used_bytes", `jvm_memory_type="heap"`) + ")",
		HeadlinePostgresConn: "sum(" + sel("db_client_connections_usage", `state="used"`) + ")",
	}
}

// SchemaVersion is bumped on a wire-breaking change (rename/removal of a
// Headline); adding one, or changing PromQL behind an unchanged key, is not.
const SchemaVersion = 1
