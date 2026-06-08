package localnet

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/metricsq"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// fakePromHandler is a minimal Prometheus stand-in for the CLI's
// metrics scraper. Returns hard-coded values keyed off the headline
// query so the test can assert on rendered text without depending
// on a real container.
func fakePromHandler(t *testing.T, vals map[metricsq.Headline]float64) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("query")
		var v *float64
		for h, query := range metricsq.SummaryQueries {
			if query == q {
				if got, ok := vals[h]; ok {
					vv := got
					v = &vv
				}
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if v == nil {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		body := `{"status":"success","data":{"resultType":"vector","result":[[1,` +
			strconv.Quote(strconv.FormatFloat(*v, 'f', -1, 64)) + `]]}}`
		_, _ = w.Write([]byte(body))
	})
}

// TestScrapeMetrics_PopulatesLatencyBlock pins the p50/p95/p99 fan-out:
// scrapeMetrics should run every SummaryQuery and project the three
// quantiles into the milliseconds-scaled LatencyReport block.
func TestScrapeMetrics_PopulatesLatencyBlock(t *testing.T) {
	srv := httptest.NewServer(fakePromHandler(t, map[metricsq.Headline]float64{
		metricsq.HeadlineLedgerTPS:    12.5,
		metricsq.HeadlineMediatorP50:  0.012, // 12 ms
		metricsq.HeadlineMediatorP95:  0.045, // 45 ms
		metricsq.HeadlineMediatorP99:  0.120, // 120 ms
		metricsq.HeadlineHeapUsed:     1.5e9,
		metricsq.HeadlinePostgresConn: 17,
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := scrapeMetrics(ctx, host, port)
	if err != nil {
		t.Fatalf("scrapeMetrics: %v", err)
	}
	if report.Latency.P50Ms == nil || *report.Latency.P50Ms < 11.9 || *report.Latency.P50Ms > 12.1 {
		t.Errorf("p50 = %v, want ~12 ms", report.Latency.P50Ms)
	}
	if report.Latency.P95Ms == nil || *report.Latency.P95Ms < 44.9 || *report.Latency.P95Ms > 45.1 {
		t.Errorf("p95 = %v, want ~45 ms", report.Latency.P95Ms)
	}
	if report.Latency.P99Ms == nil || *report.Latency.P99Ms < 119.9 || *report.Latency.P99Ms > 120.1 {
		t.Errorf("p99 = %v, want ~120 ms", report.Latency.P99Ms)
	}
}

// TestRenderMetricsText_IncludesLatencyAndGrafana confirms the text
// renderer prints the Latency block (p50/p95/p99) and a Grafana URL
// when one is populated. The CLI's UX contract is checked here so a
// silent regression to the previous "p95 only" layout fails fast.
func TestRenderMetricsText_IncludesLatencyAndGrafana(t *testing.T) {
	p50 := 12.0
	p95 := 45.0
	p99 := 120.0
	tps := 12.5
	report := &MetricsReport{
		SchemaVersion: metricsq.SchemaVersion,
		LedgerTPS:     &tps,
		Latency:       LatencyReport{P50Ms: &p50, P95Ms: &p95, P99Ms: &p99},
		Dashboards:    DashboardsBlock{GrafanaURL: "http://localhost:3001/d/canton-localnet-v1"},
	}
	var buf bytes.Buffer
	renderMetricsText(&buf, "demo", "127.0.0.1", 9090, report)
	out := buf.String()
	for _, want := range []string{"Latency:", "p50", "p95", "p99",
		"Dashboards:", "Grafana", "http://localhost:3001/d/canton-localnet-v1"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\n%s", want, out)
		}
	}
}

// TestRenderMetricsText_GrafanaHintWhenObsOff: with no Grafana URL
// the renderer should still print a Dashboards block, but with the
// "enable observability" hint so the operator knows the profile is
// off.
func TestRenderMetricsText_GrafanaHintWhenObsOff(t *testing.T) {
	report := &MetricsReport{SchemaVersion: metricsq.SchemaVersion}
	var buf bytes.Buffer
	renderMetricsText(&buf, "demo", "127.0.0.1", 9090, report)
	out := buf.String()
	if !strings.Contains(out, "Dashboards:") {
		t.Errorf("expected Dashboards section even when obs off; got\n%s", out)
	}
	if !strings.Contains(out, "enable observability profile") {
		t.Errorf("expected obs-off hint; got\n%s", out)
	}
}

// TestMetricsReport_JSONShape pins the JSON envelope so CI assertions
// (`jq .latency.p99_ms`) won't break silently. We marshal a populated
// report and unmarshal into a generic map to inspect the keys.
func TestMetricsReport_JSONShape(t *testing.T) {
	p50, p95, p99 := 1.0, 2.0, 3.0
	report := &MetricsReport{
		SchemaVersion: metricsq.SchemaVersion,
		Latency:       LatencyReport{P50Ms: &p50, P95Ms: &p95, P99Ms: &p99},
		Dashboards:    DashboardsBlock{GrafanaURL: "http://localhost:3001/d/canton-localnet-v1"},
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	lat, ok := m["latency"].(map[string]any)
	if !ok {
		t.Fatalf("missing latency block; got %s", b)
	}
	for _, k := range []string{"p50_ms", "p95_ms", "p99_ms"} {
		if _, ok := lat[k]; !ok {
			t.Errorf("latency.%s missing; got %s", k, b)
		}
	}
	dash, ok := m["dashboards"].(map[string]any)
	if !ok {
		t.Fatalf("missing dashboards block; got %s", b)
	}
	if dash["grafana_url"] != "http://localhost:3001/d/canton-localnet-v1" {
		t.Errorf("dashboards.grafana_url = %v, want canton-localnet deep link", dash["grafana_url"])
	}
}

// TestGrafanaURLFor: the URL is only emitted when state.Ports has a
// grafana_ui entry (signal that the obs profile is on). The dashboard
// UID is the one we provision under assets/grafana/dashboards/.
func TestGrafanaURLFor(t *testing.T) {
	t.Run("obs-on", func(t *testing.T) {
		state := &registry.State{Ports: map[string]int{"grafana_ui": 3001}}
		got := grafanaURLFor(state)
		want := "http://localhost:3001/d/canton-localnet-v1"
		if got != want {
			t.Errorf("grafanaURLFor = %q, want %q", got, want)
		}
	})
	t.Run("obs-off-no-port", func(t *testing.T) {
		state := &registry.State{Ports: map[string]int{}}
		if got := grafanaURLFor(state); got != "" {
			t.Errorf("grafanaURLFor with no grafana_ui = %q, want empty", got)
		}
	})
	t.Run("obs-off-zero-port", func(t *testing.T) {
		state := &registry.State{Ports: map[string]int{"grafana_ui": 0}}
		if got := grafanaURLFor(state); got != "" {
			t.Errorf("grafanaURLFor with zero port = %q, want empty", got)
		}
	})
}

func TestResolvePrometheusEndpoint_UsesRecordedRegistryPort(t *testing.T) {
	old := metricsContainersList
	metricsContainersList = func(context.Context, string) ([]containers.Info, error) {
		t.Fatal("should not probe docker when prometheus_ui is already recorded")
		return nil, nil
	}
	t.Cleanup(func() { metricsContainersList = old })

	state := &registry.State{
		Name:          "demo",
		ComposeProject: "demo",
		Ports:         map[string]int{"prometheus_ui": 19190},
	}
	host, port, err := resolvePrometheusEndpoint(context.Background(), state, "127.0.0.1", 0)
	if err != nil {
		t.Fatalf("resolvePrometheusEndpoint: %v", err)
	}
	if host != "127.0.0.1" || port != 19190 {
		t.Fatalf("got %s:%d, want 127.0.0.1:19190", host, port)
	}
}

func TestResolvePrometheusEndpoint_MissingRegistryPortWithRunningContainer(t *testing.T) {
	old := metricsContainersList
	metricsContainersList = func(context.Context, string) ([]containers.Info, error) {
		return []containers.Info{{Service: "prometheus", State: "running"}}, nil
	}
	t.Cleanup(func() { metricsContainersList = old })

	state := &registry.State{
		Name:           "demo",
		ComposeProject: "demo",
		Ports:          map[string]int{},
	}
	_, _, err := resolvePrometheusEndpoint(context.Background(), state, "127.0.0.1", 0)
	if err == nil {
		t.Fatal("expected error when prometheus runs but port is not recorded")
	}
	msg := err.Error()
	if !strings.Contains(msg, "host port was not recorded") || !strings.Contains(msg, "restart the instance") {
		t.Fatalf("unexpected error: %v", err)
	}
}
