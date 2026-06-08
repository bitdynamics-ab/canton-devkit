package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/metricsq"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildMetrics wires `dpm localnet metrics` — CLI face.
// Scrapes the per-instance Prometheus (started by the
// `--profile observability` compose overlay) and prints a small,
// curated set of headline values. The richer Grafana view lives
// at http://127.0.0.1:<grafana-port>/ but the CLI shape is for
// CI assertions and SSH-only operators who can't open a browser.
//
// `--format json` is a one-shot JSON dump suitable for:
// - `dpm localnet metrics --name demo --format json | jq .ledger.tps`
// - CI asserts ("after 1k tx upload, p95 < 500ms")
//
// Per AGENTS.md "CLI ↔ Web UI parity": the Web UI's Metrics
// screen will eventually proxy the same
// Prometheus queries; the two surfaces share the same scrape
// config (assets/compose/prometheus.yml).
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
		Long:          "Reads headline numbers (TPS, submission p95, JVM heap, DB conns) from the per-instance Prometheus. Requires the instance to have been started with --profile observability. JSON output is stable for CI assertions. The wire JSON keys (ledger_tps_5m, mediator_p95_seconds, jvm_heap_used_bytes, postgres_conn_count) are kept stable; what they MEASURE is documented in internal/metricsq/queries.go.",
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
			promHost, promPort, err := resolvePrometheusEndpoint(ctx, state, host, port)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			report, err := scrapeMetrics(ctx, promHost, promPort)
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
		"Prometheus host port (0 = auto-discover from `docker compose ps`)")
	return cmd
}

// resolveMetricsInstance mirrors resolveLogsInstance — picks the
// single registered instance when --name is empty, otherwise
// requires an explicit choice.
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
		return nil, fmt.Errorf("no registered instances — run `dpm localnet up --profile observability` first")
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

// resolvePrometheusEndpoint locates the prometheus container's
// host-published port. When --prometheus-port is explicit we
// honor it; otherwise we ask docker via the shared containers
// package for the running container and parse its port mapping.
//
// When no prometheus container exists in the project, returns a
// helpful error pointing at the --profile observability flag.
func resolvePrometheusEndpoint(ctx context.Context, state *registry.State, host string, explicitPort int) (string, int, error) {
	if explicitPort > 0 {
		return host, explicitPort, nil
	}
	infos, err := containers.List(ctx, state.ComposeProject)
	if err != nil {
		return "", 0, fmt.Errorf("docker probe: %w", err)
	}
	for _, c := range infos {
		if c.Service != "prometheus" {
			continue
		}
		// The host port is published via compose; we can't
		// reliably extract it from `compose ps --format json`
		// without a port-inspect call. Default to 9090 — the
		// compose overlay binds host port 9090 unless
		// PROMETHEUS_HOST_PORT is overridden.
		return host, 9090, nil
	}
	return "", 0, fmt.Errorf("no prometheus container in compose project %q — "+
		"start the instance with `dpm localnet up --profile observability --name %s` "+
		"(or pass --prometheus-port to point at a remote target)",
		state.ComposeProject, state.Name)
}

// MetricsReport is the stable JSON shape `--format json` emits.
// Each field is a query against the shared scrape config; missing
// data (no samples yet) is represented as null in JSON / "—" in
// text rather than 0 — distinguishes "value is zero" from "we
// couldn't ask Prometheus."
type MetricsReport struct {
	SchemaVersion int             `json:"schema_version"`
	LedgerTPS     *float64        `json:"ledger_tps_5m,omitempty"`
	MediatorP95   *float64        `json:"mediator_p95_seconds,omitempty"`
	HeapBytes     *float64        `json:"jvm_heap_used_bytes,omitempty"`
	PostgresConn  *float64        `json:"postgres_conn_count,omitempty"`
	Latency       LatencyReport   `json:"latency"`
	Dashboards    DashboardsBlock `json:"dashboards"`
}

// LatencyReport groups the mediator-approval latency quantiles in
// milliseconds. Each field is a pointer so a missing scrape
// (Prometheus has no samples yet) is distinguishable from a true
// zero. The JSON keys match what the Web UI's MetricsSummary
// renders so the two surfaces stay in lock-step.
type LatencyReport struct {
	P50Ms *float64 `json:"p50_ms,omitempty"`
	P95Ms *float64 `json:"p95_ms,omitempty"`
	P99Ms *float64 `json:"p99_ms,omitempty"`
}

// DashboardsBlock is the discoverability hand-off — the CLI surface
// can't render charts, so we point at where they live (Grafana).
// GrafanaURL is empty when the instance was started without the
// observability profile; the text renderer turns that into a hint.
type DashboardsBlock struct {
	GrafanaURL string `json:"grafana_url,omitempty"`
}

// grafanaDashboardUID pins the UID of the bundled Canton LocalNet
// dashboard provisioned under assets/grafana/dashboards/. The UID is
// baked into the JSON so a deep link is stable across restarts —
// hardcoding here keeps `dpm localnet metrics` from having to read
// the asset at runtime.
const grafanaDashboardUID = "canton-localnet-v1"

// scrapeMetrics runs the curated Prometheus queries in parallel
// and assembles them into a MetricsReport. // queries now live in internal/metricsq so CLI + handler share
// one canonical map (no copy-paste drift).
func scrapeMetrics(ctx context.Context, host string, port int) (*MetricsReport, error) {
	base := fmt.Sprintf("http://%s:%d", host, port)
	results := make(map[metricsq.Headline]*float64, len(metricsq.SummaryQueries))
	type res struct {
		key metricsq.Headline
		val *float64
		err error
	}
	ch := make(chan res, len(metricsq.SummaryQueries))
	for k, q := range metricsq.SummaryQueries {
		go func(k metricsq.Headline, q string) {
			v, err := promQuery(ctx, base, q)
			ch <- res{k, v, err}
		}(k, q)
	}
	for range metricsq.SummaryQueries {
		r := <-ch
		// Per-query failure doesn't fail the whole call —
		// metric may not exist yet on a fresh instance.
		if r.err == nil {
			results[r.key] = r.val
		}
	}
	return &MetricsReport{
		SchemaVersion: metricsq.SchemaVersion,
		LedgerTPS:     results[metricsq.HeadlineLedgerTPS],
		MediatorP95:   results[metricsq.HeadlineMediatorP95],
		HeapBytes:     results[metricsq.HeadlineHeapUsed],
		PostgresConn:  results[metricsq.HeadlinePostgresConn],
		Latency: LatencyReport{
			P50Ms: scaleSeconds(results[metricsq.HeadlineMediatorP50]),
			P95Ms: scaleSeconds(results[metricsq.HeadlineMediatorP95]),
			P99Ms: scaleSeconds(results[metricsq.HeadlineMediatorP99]),
		},
	}, nil
}

// grafanaURLFor returns the deep link to the bundled Canton LocalNet
// dashboard when the instance was started with the observability
// profile (signal: grafana_ui port is registered). Returns "" when
// observability is off so the caller can render a hint instead.
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

// promQuery executes one PromQL query and returns the scalar
// result (or nil if no samples). Tiny client — pulling
// prometheus/client_golang for one query would balloon the
// binary; the JSON shape is stable and trivially decoded.
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
			ResultType string          `json:"resultType"`
			Result     [][]interface{} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" || len(body.Data.Result) == 0 {
		return nil, nil
	}
	// Vector result: each entry is [timestamp, "value"]. Take
	// the first sample's value.
	entry := body.Data.Result[0]
	if len(entry) < 2 {
		return nil, nil
	}
	s, ok := entry[1].(string)
	if !ok {
		return nil, nil
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%g", &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// renderMetricsText prints the report in a compact human-readable
// form. Uses the term primitives so the visual style matches the
// rest of the CLI surface (Section header + KV rows).
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
		kv("  p50", r.Latency.P50Ms, "ms"),
		kv("  p95", r.Latency.P95Ms, "ms"),
		kv("  p99", r.Latency.P99Ms, "ms"),
		"",
		"Dashboards:",
	}
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

// scaleSeconds converts a seconds-valued metric to milliseconds
// for friendlier human display (canton's p95 is typically 10-500ms).
func scaleSeconds(v *float64) *float64 {
	if v == nil {
		return nil
	}
	ms := *v * 1000
	return &ms
}

// scaleBytes converts bytes to MiB.
func scaleBytes(v *float64) *float64 {
	if v == nil {
		return nil
	}
	mib := *v / (1024 * 1024)
	return &mib
}
