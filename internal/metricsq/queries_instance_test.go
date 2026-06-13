package metricsq

import "testing"

// TestSummaryQueriesFor_Unscoped pins that the empty-instance form
// reproduces the curated, probe-verified PromQL byte-for-byte — so the
// per-instance Prometheus path and the metric-name fix are unchanged.
func TestSummaryQueriesFor_Unscoped(t *testing.T) {
	want := map[Headline]string{
		HeadlineLedgerTPS:    "sum(rate(daml_participant_api_indexer_updates[5m])) or vector(0)",
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

// TestSummaryQueriesFor_Scoped pins that an instance injects a valid
// instance="X" matcher into every selector — including composing with a
// metric's existing label without an invalid trailing comma — so the
// shared multi-instance Prometheus headline reflects one instance.
func TestSummaryQueriesFor_Scoped(t *testing.T) {
	got := SummaryQueriesFor("demo")
	cases := map[Headline]string{
		// no existing label -> braces added with just the instance matcher
		HeadlineLedgerTPS: `sum(rate(daml_participant_api_indexer_updates{instance="demo"}[5m])) or vector(0)`,
		// bucket inside histogram_quantile gets scoped too
		HeadlineMediatorP95: `histogram_quantile(0.95, sum(rate(daml_sequencer_client_submissions_sequencing_duration_seconds_bucket{instance="demo"}[5m])) by (le))`,
		// existing label -> instance matcher composed before it, comma-joined
		HeadlineHeapUsed:     `sum(jvm_memory_used_bytes{instance="demo",jvm_memory_type="heap"})`,
		HeadlinePostgresConn: `sum(db_client_connections_usage{instance="demo",state="used"})`,
	}
	for k, w := range cases {
		if got[k] != w {
			t.Errorf("SummaryQueriesFor(\"demo\")[%s] =\n  %q\nwant\n  %q", k, got[k], w)
		}
	}
}
