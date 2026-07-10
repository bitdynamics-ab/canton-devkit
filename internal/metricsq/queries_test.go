package metricsq

import (
	"strings"
	"testing"
)

// Membership change = wire-breaking JSON change; pin it so it's deliberate.
func TestSummaryQueries_AllHeadlinesPresent(t *testing.T) {
	want := []Headline{
		HeadlineLedgerTPS,
		HeadlineMediatorAvg,
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
		// Same bucket source across all three, else the percentiles are incomparable.
		if !strings.Contains(q, "daml_sequencer_client_submissions_sequencing_duration_seconds_bucket[5m]") {
			t.Errorf("query for %s must share the sequencer submission-duration histogram; got %q", c.h, q)
		}
		if !strings.Contains(q, "by (le)") {
			t.Errorf("query for %s missing `by (le)` grouping needed by histogram_quantile; got %q", c.h, q)
		}
	}
}

// The avg must be the histogram mean (_sum/_count), which stays exact when
// the percentiles go NaN on +Inf-only buckets — never a histogram_quantile.
func TestMediatorAvg_UsesSumOverCount(t *testing.T) {
	q := SummaryQueries[HeadlineMediatorAvg]
	base := "daml_sequencer_client_submissions_sequencing_duration_seconds"
	if !strings.Contains(q, base+"_sum") {
		t.Errorf("avg query must rate the histogram's _sum series; got %q", q)
	}
	if !strings.Contains(q, base+"_count") {
		t.Errorf("avg query must divide by the histogram's _count series; got %q", q)
	}
	if !strings.Contains(q, "/") {
		t.Errorf("avg query must be a _sum/_count ratio; got %q", q)
	}
	if strings.Contains(q, "histogram_quantile") {
		t.Errorf("avg query must not use histogram_quantile (that is NaN on +Inf-only histograms); got %q", q)
	}
}
