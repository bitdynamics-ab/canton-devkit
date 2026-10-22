package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func snapshotNotFoundRemediation(t *testing.T) []string {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := snapshotMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/ghost/snapshot", "application/octet-stream", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	var body errorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Remediation
}

func TestSnapshot_NotFoundHintDefaultsToDPM(t *testing.T) {
	got := snapshotNotFoundRemediation(t)
	want := "Run `dpm localnet list` to see available instances."
	if len(got) != 1 || got[0] != want {
		t.Errorf("remediation = %q, want [%q]", got, want)
	}
}

func TestSnapshot_NotFoundHintFollowsDirectPrefix(t *testing.T) {
	t.Cleanup(cliprefix.Set("canton-devkit localnet"))
	got := snapshotNotFoundRemediation(t)
	want := "Run `canton-devkit localnet list` to see available instances."
	if len(got) != 1 || got[0] != want {
		t.Errorf("remediation = %q, want [%q]", got, want)
	}
	if strings.Contains(strings.Join(got, " "), "dpm localnet") {
		t.Errorf("direct launch must not hint dpm localnet, got %q", got)
	}
}

func TestContracts_MissingPortHintFollowsPrefix(t *testing.T) {
	for _, tc := range []struct{ set, prefix, other string }{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			if tc.set != "" {
				t.Cleanup(cliprefix.Set(tc.set))
			}
			t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
			seedInstance(t, "demo", "0.6.4", map[string]int{"app_user_ui": 4485}, registry.StatusRunning)
			srv := contractsTxMux(t)
			resp, err := http.Get(srv.URL + "/api/instances/demo/contracts?role=app-user")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			var body errorBody
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			hint := strings.Join(body.Remediation, " ")
			for _, want := range []string{"`" + tc.prefix + " down demo`", "`" + tc.prefix + " up demo`"} {
				if !strings.Contains(hint, want) {
					t.Errorf("remediation %q missing %q", hint, want)
				}
			}
			if strings.Contains(hint, tc.other) {
				t.Errorf("remediation %q must not mention %q", hint, tc.other)
			}
		})
	}
}
