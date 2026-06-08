//go:build integration

// smoke_test.go is the BIT-232 drift guard: every Headline query in
// SummaryQueries must resolve against a real Splice Canton's
// Prometheus and return at least one sample. The unit-test layer
// can only verify the map is structurally sound; this file is the
// only thing that catches "we shipped a PromQL string that returns
// silent no-data because some upstream metric got renamed."
//
// Behind `//go:build integration` so `go test ./...` in regular CI
// stays hermetic. Run with:
//
//	# Bring up the LocalNet yourself (the test does NOT manage it —
//	# obs profile takes ~7 min on a cold cache and would dominate
//	# the test wall-clock):
//	canton-devkit localnet up --name metric-audit --profile observability
//	PROM_PORT=$(canton-devkit localnet status --name metric-audit --format json \
//	  | jq -r '.endpoints[] | select(.label=="prometheus_ui") | .port')
//	METRICSQ_SMOKE_PROM=http://localhost:${PROM_PORT} \
//	  go test -tags=integration -run TestSummaryQueries_LiveProm \
//	  ./internal/metricsq/
//	canton-devkit localnet down --name metric-audit
//
// We don't auto-spawn the LocalNet here because:
//   - It pulls multi-GB images on first run; CI would time out
//     before the test even started.
//   - There may already be a LocalNet running on the developer's
//     box that they want to point this at.
//
// METRICSQ_SMOKE_PROM is required: missing env → test is SKIPPED so
// `go test -tags=integration ./...` on a box without a LocalNet
// running stays green. Set it to fail-fast in CI.
package metricsq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestSummaryQueries_LiveProm queries each Headline against the
// Prometheus URL in METRICSQ_SMOKE_PROM and asserts the result
// vector is non-empty. A query that returns 0 results means the
// underlying metric name has drifted upstream (Splice / Daml SDK
// release renamed it) or the scrape config dropped the relevant
// job — either way we want to FAIL LOUDLY rather than silently
// surface "no data" on the CLI / Web UI.
func TestSummaryQueries_LiveProm(t *testing.T) {
	base := os.Getenv("METRICSQ_SMOKE_PROM")
	if base == "" {
		t.Skip("METRICSQ_SMOKE_PROM not set; bring up `canton-devkit localnet up --profile observability` and export it to http://localhost:<prom-port>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	for headline, query := range SummaryQueries {
		// Capture loop vars for the parallel subtests. (Go 1.22+
		// fixes this implicitly but the repo still pins 1.26 and
		// being explicit costs nothing.)
		headline, query := headline, query
		t.Run(string(headline), func(t *testing.T) {
			t.Parallel()
			n, err := queryResultCount(ctx, base, query)
			if err != nil {
				t.Fatalf("query %q failed: %v", query, err)
			}
			if n == 0 {
				t.Errorf("headline %s returned 0 results — metric name likely drifted upstream. Query: %s", headline, query)
			}
		})
	}
}

// queryResultCount runs one PromQL against the instant-query API
// and returns len(data.result). Tiny replica of the client in
// internal/cli/localnet/metrics.go to keep this file dependency-
// free (no shared test helpers across packages for an integration
// smoke).
func queryResultCount(ctx context.Context, base, query string) (int, error) {
	u := base + "/api/v1/query?" + url.Values{"query": {query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus returned %s", resp.Status)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, err
	}
	if body.Status != "success" {
		return 0, fmt.Errorf("prometheus status=%s", body.Status)
	}
	return len(body.Data.Result), nil
}
