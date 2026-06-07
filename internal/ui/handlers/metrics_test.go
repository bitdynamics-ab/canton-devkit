package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestMetrics_RejectsMissingQuery pins the input
// validation: ?query is required. Without it the handler returns
// 400 with the structured INVALID_REQUEST code so the frontend
// can show a friendly prompt rather than a generic 5xx.
func TestMetrics_RejectsMissingQuery(t *testing.T) {
	mux := http.NewServeMux()
	MountMetrics(mux)
	req := httptest.NewRequest(http.MethodGet,
		"/api/instances/demo/metrics", nil)
	req.SetPathValue("name", "demo")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing query") {
		t.Errorf("body should mention missing query; got %s", rec.Body.String())
	}
}

// TestMetrics_RejectsMalformedQuery: the PromQL regex allowlist
// guards against shell-metachar smuggling. A `;rm -rf /` style
// payload must fail at validation, BEFORE we URL-encode and hand
// to Prometheus (defence-in-depth — encoding alone isn't enough
// if the eventual proxy logs the un-encoded form, etc).
func TestMetrics_RejectsMalformedQuery(t *testing.T) {
	mux := http.NewServeMux()
	MountMetrics(mux)
	badQueries := []string{
		"up;rm -rf /", // shell semicolon
		"up\nwhoami",  // newline injection
		"up`whoami`",  // backticks
		"up$(whoami)", // command sub
		"up\x00null",  // NUL byte
	}
	for _, q := range badQueries {
		t.Run(q, func(t *testing.T) {
			// URL-encode the query parameter so the test request
			// itself is valid HTTP — the regex check happens
			// AFTER url.Values decoding, so the encoded form
			// reaches the validator intact.
			u := "/api/instances/demo/metrics?" + url.Values{"query": {q}}.Encode()
			req := httptest.NewRequest(http.MethodGet, u, nil)
			req.SetPathValue("name", "demo")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("query %q: status = %d, want 400", q, rec.Code)
			}
		})
	}
}

// TestMetrics_RejectsInvalidInstanceName: name has to be an RFC
// 1123 DNS label (registry.ValidateName). A name with uppercase
// or invalid chars must fail at the handler validation layer.
// Note: path-traversal-shaped names (`../passwd`) are caught by
// the http.ServeMux URL-cleanup pass BEFORE reaching the handler
// (returns 307 redirect to the cleaned path), so we test with
// names that have valid path shape but invalid DNS-label content.
func TestMetrics_RejectsInvalidInstanceName(t *testing.T) {
	mux := http.NewServeMux()
	MountMetrics(mux)
	req := httptest.NewRequest(http.MethodGet,
		"/api/instances/UPPER_CASE/metrics?query=up", nil)
	req.SetPathValue("name", "UPPER_CASE")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid DNS-label name", rec.Code)
	}
}

// TestMetricsSummary_RejectsInvalidInstanceName same shape as above.
func TestMetricsSummary_RejectsInvalidInstanceName(t *testing.T) {
	mux := http.NewServeMux()
	MountMetrics(mux)
	// CAPS in DNS label — invalid but URL-safe (no need to encode).
	req := httptest.NewRequest(http.MethodGet,
		"/api/instances/HASCAPS/metrics/summary", nil)
	req.SetPathValue("name", "HASCAPS")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid DNS-label name", rec.Code)
	}
}

// (Removed TestIntToString: the helper was reinventing
// strconv.Itoa — replaced with the stdlib call in v4. Pinning
// strconv's own correctness via our test is redundant.)

// TestMetrics_PromQLAllowlistAccepts_HappyPath: valid PromQL
// passes the regex. We don't test the actual proxy call (that'd
// require a live Prometheus); just that the validation layer
// admits real queries.
func TestMetrics_PromQLAllowlistAccepts_HappyPath(t *testing.T) {
	goodQueries := []string{
		"up",
		"rate(canton_participant_transactions_total[5m])",
		`up{instance="demo"}`,
		"sum(jvm_memory_used_bytes{area=\"heap\"})",
		"histogram_quantile(0.95, sum(rate(x_bucket[5m])) by (le))",
	}
	for _, q := range goodQueries {
		if !promQueryRE.MatchString(q) {
			t.Errorf("PromQL %q rejected by allowlist; expected pass", q)
		}
	}
}

// TestMetricsSummary_ResponseShapeStable: the JSON envelope MUST
// always carry schema_version and the instance/metrics keys.
// Frontend depends on these fields existing even when no metrics
// have been gathered yet.
func TestMetricsSummary_ResponseShapeStable(t *testing.T) {
	// We can't test the success path without a live Prometheus +
	// registered instance. But we CAN test the not-found path
	// produces a structured error envelope (not raw text).
	mux := http.NewServeMux()
	MountMetrics(mux)
	req := httptest.NewRequest(http.MethodGet,
		"/api/instances/nonexistent/metrics/summary", nil)
	req.SetPathValue("name", "nonexistent")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	// Should be 404 (registry not found) with JSON body.
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Errorf("body must be JSON; got %s", rec.Body.String())
	}
	if _, ok := body["code"]; !ok {
		t.Error("error response missing structured code")
	}
}
