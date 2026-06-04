package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFeatureForPath pins the path→feature mapping for the M2 adoption
// signal: real screen endpoints map to their feature, infra endpoints map
// to "" (recorded as nothing).
func TestFeatureForPath(t *testing.T) {
	cases := map[string]string{
		"/api/dars":            "dar",
		"/api/dar/abc/info":    "dar",
		"/api/contracts":       "explorer",
		"/api/transactions":    "explorer",
		"/api/metrics/summary": "metrics",
		"/api/tokens":          "tokens",
		"/api/tokens/RTK/mint": "tokens",
		"/api/skills":          "skills",
		"/api/snapshots":       "backup",
		"/api/instances":       "instances",
		"/api/version":         "", // infra
		"/api/auth/token":      "", // infra
		"/healthz":             "", // infra
		"/events":              "", // infra
		"/assets/index.js":     "", // bundle
	}
	for path, want := range cases {
		if got := featureForPath(path); got != want {
			t.Errorf("featureForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestWithFeatureTelemetry_RecordsAndPassesThrough verifies the
// middleware forwards the request to the wrapped handler (no behavior
// change) and only acts on recognized feature paths.
func TestWithFeatureTelemetry_RecordsAndPassesThrough(t *testing.T) {
	var reached bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	mw := withFeatureTelemetry(inner)

	// A feature path and an infra path both pass through unchanged;
	// telemetry is a no-op here (no sink installed) so we assert only
	// transparency — the recording itself is covered in the telemetry
	// package's IncOnce test.
	for _, path := range []string{"/api/dars", "/api/version"} {
		reached = false
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if !reached {
			t.Errorf("middleware did not forward request for %q", path)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status for %q = %d, want 200", path, rec.Code)
		}
	}
}
