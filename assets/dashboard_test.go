package assets

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDashboardJSONIsValid guards the embedded Grafana dashboard against
// accidental syntax breakage — Grafana silently ignores a malformed
// dashboard on provisioning, so a CI-side parse check is the cheapest
// way to catch regressions before they ship.
func TestDashboardJSONIsValid(t *testing.T) {
	raw, err := FS.ReadFile("grafana/dashboards/canton-localnet.json")
	if err != nil {
		t.Fatalf("read embedded dashboard: %v", err)
	}
	var dash struct {
		Panels []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("dashboard JSON does not parse: %v", err)
	}
	if len(dash.Panels) == 0 {
		t.Fatal("dashboard has no panels")
	}
}

// TestDashboardHasACSAndThroughputPanels pins the two live-audited
// panels added to satisfy the completeness review. Stock Splice
// 0.6.4 does not expose exact ACS cardinality or template-grain
// submission counters via Prometheus, so these panel titles and
// queries must stay honest about the signals they actually show.
func TestDashboardHasACSAndThroughputPanels(t *testing.T) {
	raw, err := FS.ReadFile("grafana/dashboards/canton-localnet.json")
	if err != nil {
		t.Fatalf("read embedded dashboard: %v", err)
	}
	var dash struct {
		Panels []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("parse dashboard: %v", err)
	}
	want := map[int]string{
		14: "ACS Lookup Buffer Length",
		15: "Top 10 gRPC Methods by Throughput (ops/s, 5m)",
	}
	got := map[int]string{}
	for _, p := range dash.Panels {
		got[p.ID] = p.Title
	}
	for id, title := range want {
		if got[id] != title {
			t.Errorf("expected panel id=%d title=%q, got title=%q", id, title, got[id])
		}
	}
}

func TestDashboardDoesNotUseObsoleteMetricNames(t *testing.T) {
	raw, err := FS.ReadFile("grafana/dashboards/canton-localnet.json")
	if err != nil {
		t.Fatalf("read embedded dashboard: %v", err)
	}
	s := string(raw)
	for _, obsolete := range []string{
		"daml_services_index_active_contracts",
		"daml_commands_submissions_total",
		"template_id",
	} {
		if strings.Contains(s, obsolete) {
			t.Errorf("dashboard still references obsolete/non-live metric token %q", obsolete)
		}
	}
}
