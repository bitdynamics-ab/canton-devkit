package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/metricsq"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

var metricsContainersList = containers.List

// buildMetrics wires `dpm localnet metrics`: scrapes the instance's
// Prometheus and prints a curated set of headline values. The JSON shape is
// for CI assertions; the Web UI Metrics screen shares the same scrape config.
func buildMetrics() *cobra.Command {
	var (
		instance string
		format   string
		host     string
		port     int
	)
	cmd := &cobra.Command{
		Use:           "metrics",
		Short:         "Scrape live metrics from an instance's Prometheus",
		Long:          "Reads headline numbers (TPS, sequencing latency, JVM heap, DB conns) from the per-instance Prometheus. Requires the instance to have been started with --profile observability. JSON output is stable for CI assertions. The wire JSON keys (ledger_tps_5m, mediator_p95_seconds, jvm_heap_used_bytes, postgres_conn_count) are kept stable; what they MEASURE is documented in internal/metricsq/queries.go.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				return err
			}
			state, err := resolveMetricsInstance(instance)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			promHost, promPort, promScope, err := resolvePrometheusEndpoint(ctx, state, host, port)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			report, err := scrapeMetrics(ctx, promHost, promPort, promScope)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"prometheus query failed: "+err.Error())
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			report.Dashboards.GrafanaURL = grafanaURLFor(state)
			if format == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			renderMetricsText(cmd.OutOrStdout(), state.Name, promHost, promPort, report)
			return nil
		},
	}
	cmd.Flags().StringVar(&instance, "name", "",
		"Instance to read metrics from (defaults to the only registered instance)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&host, "prometheus-host", "127.0.0.1",
		"Prometheus host (set when running against a remote scrape target)")
	cmd.Flags().IntVar(&port, "prometheus-port", 0,
		"Prometheus host port (0 = auto-discover from the instance registry)")
	return cmd
}

// resolveMetricsInstance picks the single registered instance when --name is
// empty, otherwise requires an explicit choice.
func resolveMetricsInstance(name string) (*registry.State, error) {
	if name != "" {
		return resolveInstance(name)
	}
	idx, err := registry.ReadIndex()
	if err != nil {
		return nil, fmt.Errorf("read registry index: %w", err)
	}
	switch len(idx.Entries) {
	case 0:
		return nil, fmt.Errorf("no registered instances — run `dpm localnet up <name> --profile observability` first")
	case 1:
		return registry.Read(idx.Entries[0].Name)
	default:
		names := make([]string, 0, len(idx.Entries))
		for _, e := range idx.Entries {
			names = append(names, e.Name)
		}
		return nil, fmt.Errorf("multiple instances registered (%s) — pass --name to pick one",
			strings.Join(names, ", "))
	}
}

// resolvePrometheusEndpoint locates the Prometheus to scrape and the scope to
// filter by. It prefers the SHARED host-level stack (returning the instance
// name so queries filter by instance=<name>), else falls back to the
// per-instance Prometheus (unscoped, since it holds one instance). Docker is
// probed only when no port is recorded, to tell "obs off" from "stale state".
func resolvePrometheusEndpoint(ctx context.Context, state *registry.State, host string, explicitPort int) (rhost string, rport int, scope string, rerr error) {
	if explicitPort > 0 {
		return host, explicitPort, "", nil
	}
	if localnet.InstanceObservabilityEnabled(state.Name) {
		if h, p, err := localnet.SharedPrometheusEndpoint(ctx); err == nil {
			return h, p, state.Name, nil
		}
	}
	if port, ok := state.Ports["prometheus_ui"]; ok && port > 0 {
		return host, port, "", nil
	}
	infos, err := metricsContainersList(ctx, state.ComposeProject)
	if err != nil {
		return "", 0, "", fmt.Errorf("docker probe: %w", err)
	}
	for _, c := range infos {
		if c.Service != "prometheus" {
			continue
		}
		return "", 0, "", fmt.Errorf("prometheus is running for instance %q but its host port was not recorded — "+
			"restart the instance with `dpm localnet restart %s` or pass --prometheus-port explicitly",
			state.Name, state.Name)
	}
	return "", 0, "", fmt.Errorf("no Prometheus for instance %q — "+
		"enable observability (`dpm localnet observability enable --name %s`, or bring it up with "+
		"`--profile observability`) so the shared stack scrapes it, or pass --prometheus-port to point at a remote target",
		state.Name, state.Name)
}

// MetricsReport is the stable JSON shape `--format json` emits. Missing data
// is null (not 0) so "value is zero" stays distinct from "no sample".
type MetricsReport struct {
	SchemaVersion int             `json:"schema_version"`
	LedgerTPS     *float64        `json:"ledger_tps_5m,omitempty"`
	MediatorP95   *float64        `json:"mediator_p95_seconds,omitempty"`
	HeapBytes     *float64        `json:"jvm_heap_used_bytes,omitempty"`
	PostgresConn  *float64        `json:"postgres_conn_count,omitempty"`
	Latency       LatencyReport   `json:"latency"`
	Dashboards    DashboardsBlock `json:"dashboards"`
}

// LatencyReport groups the sequencing-latency figures in milliseconds. AvgMs
// is the headline mean; the percentiles come back nil on Splice 0.6.4 whose
// histogram exports only the +Inf bucket. JSON keys match the Web UI's.
type LatencyReport struct {
	AvgMs *float64 `json:"avg_ms,omitempty"`
	P50Ms *float64 `json:"p50_ms,omitempty"`
	P95Ms *float64 `json:"p95_ms,omitempty"`
	P99Ms *float64 `json:"p99_ms,omitempty"`
}

// DashboardsBlock points at Grafana since the CLI can't render charts.
// GrafanaURL is empty when the observability profile is off.
type DashboardsBlock struct {
	GrafanaURL string `json:"grafana_url,omitempty"`
}

// grafanaDashboardUID is the bundled dashboard's UID (assets/grafana/
// dashboards/), hardcoded so the deep link is stable without reading the asset.
const grafanaDashboardUID = "canton-localnet-v1"

// scrapeMetrics runs the curated queries in parallel into a MetricsReport.
// instance scopes them to one LocalNet when non-empty.
//
// Error model: "no samples yet" (promQuery returns nil,nil) is a valid empty
// headline, not a failure. Only when EVERY query fails at the transport layer
// do we return an error — a single flaky query must not sink a report against
// a healthy Prometheus, while a real outage takes them all down together.
func scrapeMetrics(ctx context.Context, host string, port int, instance string) (*MetricsReport, error) {
	base := fmt.Sprintf("http://%s:%d", host, port)
	queries := metricsq.SummaryQueriesFor(instance)
	results := make(map[metricsq.Headline]*float64, len(queries))
	type res struct {
		key metricsq.Headline
		val *float64
		err error
	}
	ch := make(chan res, len(queries))
	for k, q := range queries {
		go func(k metricsq.Headline, q string) {
			v, err := promQuery(ctx, base, q)
			ch <- res{k, v, err}
		}(k, q)
	}
	var firstErr error
	transportFailures := 0
	for range queries {
		r := <-ch
		if r.err != nil {
			transportFailures++
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		results[r.key] = r.val
	}
	if transportFailures == len(queries) && firstErr != nil {
		return nil, firstErr
	}
	return &MetricsReport{
		SchemaVersion: metricsq.SchemaVersion,
		LedgerTPS:     results[metricsq.HeadlineLedgerTPS],
		MediatorP95:   results[metricsq.HeadlineMediatorP95],
		HeapBytes:     results[metricsq.HeadlineHeapUsed],
		PostgresConn:  results[metricsq.HeadlinePostgresConn],
		Latency: LatencyReport{
			AvgMs: scaleSeconds(results[metricsq.HeadlineMediatorAvg]),
			P50Ms: scaleSeconds(results[metricsq.HeadlineMediatorP50]),
			P95Ms: scaleSeconds(results[metricsq.HeadlineMediatorP95]),
			P99Ms: scaleSeconds(results[metricsq.HeadlineMediatorP99]),
		},
	}, nil
}

// grafanaURLFor deep-links to the bundled dashboard when the grafana_ui port
// is registered (obs on), else "" so the caller can render a hint.
func grafanaURLFor(state *registry.State) string {
	if state == nil {
		return ""
	}
	port, ok := state.Ports["grafana_ui"]
	if !ok || port == 0 {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d/d/%s", port, grafanaDashboardUID)
}

// promQuery executes one PromQL query and returns the scalar result (or nil
// if no samples).
func promQuery(ctx context.Context, base, query string) (*float64, error) {
	u := base + "/api/v1/query?" + url.Values{"query": {query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus returned %s", resp.Status)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value []interface{} `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" || len(body.Data.Result) == 0 {
		return nil, nil
	}
	entry := body.Data.Result[0].Value
	if len(entry) < 2 {
		return nil, nil
	}
	s, ok := entry[1].(string)
	if !ok {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, nil
	}
	return &v, nil
}

// renderMetricsText prints the report in a compact human-readable form.
func renderMetricsText(out io.Writer, instance, host string, port int, r *MetricsReport) {
	kv := func(k string, v *float64, unit string) string {
		val := "—"
		if v != nil {
			val = fmt.Sprintf("%.2f %s", *v, unit)
		}
		return term.KV(k, val, 22)
	}
	rows := []string{
		kv("ledger TPS (5m)", r.LedgerTPS, "tx/s"),
		kv("JVM heap used", scaleBytes(r.HeapBytes), "MiB"),
		kv("DB conns (used)", r.PostgresConn, ""),
		"",
		"Latency:",
		kv("  avg", r.Latency.AvgMs, "ms"),
	}
	// Splice 0.6.4's +Inf-only histogram yields no percentiles; print them
	// only when present rather than a row of dashes (the avg is always exact).
	if r.Latency.P50Ms != nil || r.Latency.P95Ms != nil || r.Latency.P99Ms != nil {
		rows = append(rows,
			kv("  p50", r.Latency.P50Ms, "ms"),
			kv("  p95", r.Latency.P95Ms, "ms"),
			kv("  p99", r.Latency.P99Ms, "ms"),
		)
	}
	rows = append(rows, "", "Dashboards:")
	if r.Dashboards.GrafanaURL != "" {
		rows = append(rows, term.KV("  Grafana", r.Dashboards.GrafanaURL, 22))
	} else {
		rows = append(rows, term.KV("  Grafana",
			"(enable observability profile to see dashboards)", 22))
	}
	body := strings.Join(rows, "\n")
	right := fmt.Sprintf("prometheus %s:%d", host, port)
	_, _ = fmt.Fprintln(out, term.Section("metrics · "+instance, right, body, 0))
}

// scaleSeconds converts a seconds-valued metric to milliseconds.
func scaleSeconds(v *float64) *float64 {
	if v == nil {
		return nil
	}
	ms := *v * 1000
	return &ms
}

func scaleBytes(v *float64) *float64 {
	if v == nil {
		return nil
	}
	mib := *v / (1024 * 1024)
	return &mib
}
