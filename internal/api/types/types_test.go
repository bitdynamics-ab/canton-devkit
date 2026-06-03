package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestInstance_RoundTrips proves Instance survives a marshal +
// unmarshal cycle without field loss. Cheap regression guard for
// JSON tag typos — the most common source of CLI/Web-UI shape drift.
func TestInstance_RoundTrips(t *testing.T) {
	in := Instance{
		Name:           "demo",
		SpliceVersion:  "0.6.4",
		Status:         "running",
		CreatedAt:      "2026-05-25T12:00:00Z",
		Uptime:         "1h 22m",
		ComposeProject: "canton-demo",
		Services:       []ServiceStatus{{Name: "x", State: "healthy"}},
		Endpoints:      []Endpoint{{Label: "Wallet", URL: "http://localhost:4485"}},
		Parties:        []Party{{Wallet: "alice", ID: "party::1220a8…"}},
		Credentials:    map[string]Credential{"sv": {Role: "sv", User: "sv"}},
	}
	buf, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Instance
	if err := json.Unmarshal(buf, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "demo" || out.Status != "running" || len(out.Services) != 1 {
		t.Errorf("round-trip lost data:\nin=%+v\nout=%+v", in, out)
	}
}

// TestInstance_OmitEmpty verifies optional fields don't pollute the
// JSON output for a freshly created `localnet list` entry. The Web
// UI's network panel + grep-friendly CLI both care that an empty
// Services/Endpoints slice doesn't appear as null/[] noise.
func TestInstance_OmitEmpty(t *testing.T) {
	buf, err := json.Marshal(Instance{Name: "x", Status: "stopped"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(buf)
	for _, key := range []string{"services", "endpoints", "parties", "credentials", "uptime"} {
		if strings.Contains(s, `"`+key+`"`) {
			t.Errorf("expected %q omitted on empty value, got %s", key, s)
		}
	}
}

// TestSchemaVersion_NonZero is a sanity guard against accidentally
// shipping a 0 value (which would null-coalesce in many JS readers
// and look like "not set").
func TestSchemaVersion_NonZero(t *testing.T) {
	if SchemaVersion <= 0 {
		t.Errorf("SchemaVersion must be >0, got %d", SchemaVersion)
	}
}

// TestPreflight_OmitsRemediationOnPass is the negative space of the
// friendly-error renderer (P1-12): a passing check must not carry a
// remediation list, or the CLI will helpfully suggest fixes for
// non-problems.
func TestPreflight_OmitsRemediationOnPass(t *testing.T) {
	c := PreflightCheck{Label: "Disk", Result: "pass"}
	buf, _ := json.Marshal(c)
	if strings.Contains(string(buf), "remediation") {
		t.Errorf("passing check should omit remediation, got %s", buf)
	}
}
