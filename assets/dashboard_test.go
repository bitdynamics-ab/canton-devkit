package assets

import (
	"encoding/json"
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

// TestDashboardHasACSAndTemplatePanels pins the two panels added to
// satisfy the completeness review (active contract counts +
// per-template throughput). If someone removes either panel the test
// fails, prompting them to either restore it or update the IDs here
// with a deliberate review note.
func TestDashboardHasACSAndTemplatePanels(t *testing.T) {
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
		14: "Active Contract Set Size",
		15: "Top 10 Templates by Throughput (ops/s, 5m)",
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
