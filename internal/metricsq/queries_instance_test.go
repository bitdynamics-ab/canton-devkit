package metricsq

import "testing"

// The empty-instance form must reproduce the curated PromQL byte-for-byte.
func TestSummaryQueriesFor_Unscoped(t *testing.T) {
	want := map[Headline]string{
		HeadlineLedgerTPS:    "sum(rate(daml_participant_api_indexer_updates[5m])) or vector(0)",
		HeadlineMediatorAvg:  "sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_sum[5m])) / sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_count[5m]))",
		HeadlineMediatorP50:  "histogram_quantile(0.50, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))",
		HeadlineMediatorP95:  "histogram_quantile(0.95, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))",
		HeadlineMediatorP99:  "histogram_quantile(0.99, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m])) by (le))",
		HeadlineHeapUsed:     `sum(jvm_memory_used_bytes{jvm_memory_type="heap"})`,
		HeadlinePostgresConn: `sum(db_client_connections_usage{state="used"})`,
	}
	got := SummaryQueriesFor("")
	for k, w := range want {
		if got[k] != w {
			t.Errorf("SummaryQueriesFor(\"\")[%s] =\n  %q\nwant\n  %q", k, got[k], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("query count = %d, want %d", len(got), len(want))
	}
}

// A scoped instance must inject a valid instance="X" matcher into every
// selector, composing with existing labels without a trailing comma.
func TestSummaryQueriesFor_Scoped(t *testing.T) {
	got := SummaryQueriesFor("demo")
	cases := map[Headline]string{
		HeadlineLedgerTPS:    `sum(rate(daml_participant_api_indexer_updates{instance="demo"}[5m])) or vector(0)`,
		HeadlineMediatorP95:  `histogram_quantile(0.95, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket{instance="demo"}[5m])) by (le))`,
		HeadlineMediatorAvg:  `sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_sum{instance="demo"}[5m])) / sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_count{instance="demo"}[5m]))`,
		HeadlineHeapUsed:     `sum(jvm_memory_used_bytes{instance="demo",jvm_memory_type="heap"})`,
		HeadlinePostgresConn: `sum(db_client_connections_usage{instance="demo",state="used"})`,
	}
	for k, w := range cases {
		if got[k] != w {
			t.Errorf("SummaryQueriesFor(\"demo\")[%s] =\n  %q\nwant\n  %q", k, got[k], w)
		}
	}
}
