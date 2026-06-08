package metricsq

import (
	"strings"
	"testing"
)

// TestSummaryQueries_AllHeadlinesPresent pins the curated map's
// membership: adding/removing a Headline is a wire-breaking change
// to the JSON shape both CLI and Web UI emit, so the set should be
// reviewed deliberately, not changed by accident.
func TestSummaryQueries_AllHeadlinesPresent(t *testing.T) {
	want := []Headline{
		HeadlineLedgerTPS,
		HeadlineMediatorP50,
		HeadlineMediatorP95,
		HeadlineMediatorP99,
		HeadlineHeapUsed,
		HeadlinePostgresConn,
	}
	for _, h := range want {
		if _, ok := SummaryQueries[h]; !ok {
			t.Errorf("SummaryQueries missing headline %q", h)
		}
	}
	if len(SummaryQueries) != len(want) {
		t.Errorf("SummaryQueries len = %d, want %d (unexpected addition?)",
			len(SummaryQueries), len(want))
	}
}

// TestLatencyQuantiles_WellFormed: the three histogram_quantile
// queries must share the same bucket source (so a percentile-vs-
// percentile comparison is meaningful) and pin the requested
// quantile.
func TestLatencyQuantiles_WellFormed(t *testing.T) {
	cases := []struct {
		h    Headline
		want string
	}{
		{HeadlineMediatorP50, "histogram_quantile(0.50,"},
		{HeadlineMediatorP95, "histogram_quantile(0.95,"},
		{HeadlineMediatorP99, "histogram_quantile(0.99,"},
	}
	for _, c := range cases {
		q := SummaryQueries[c.h]
		if !strings.HasPrefix(q, c.want) {
			t.Errorf("query for %s = %q, want prefix %q", c.h, q, c.want)
		}
		if !strings.Contains(q, "canton_mediator_approval_duration_bucket[5m]") {
			t.Errorf("query for %s must share the mediator approval-duration histogram; got %q", c.h, q)
		}
		if !strings.Contains(q, "by (le)") {
			t.Errorf("query for %s missing `by (le)` grouping needed by histogram_quantile; got %q", c.h, q)
		}
	}
}
