package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/metricsq"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// MountMetrics wires BIT-134's Web UI face: a per-instance
// Prometheus passthrough so the future Metrics screen can render
// live charts without the browser scraping Prometheus directly
// (avoids CORS + lets the handler enforce auth/JWT once that
// lands).
//
//   GET /api/instances/{name}/metrics?query=<PromQL>
//
// Returns Prometheus's `/api/v1/query` response verbatim when the
// scrape succeeds. Returns 503 with a structured envelope when
// the observability profile isn't running for the instance —
// frontend renders a "raise observability" remediation panel.
//
// Per AGENTS.md "CLI ↔ Web UI parity": this handler shares the
// scrape config and PromQL grammar with `dpm localnet metrics`.
// Adding a new built-in panel updates both surfaces.
func MountMetrics(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{name}/metrics", handleMetricsQuery())
	mux.HandleFunc("GET /api/instances/{name}/metrics/summary", handleMetricsSummary())
	// Range query — backs every chart on the Web UI Metrics screen.
	// Wraps Prometheus's /api/v1/query_range so the frontend gets a
	// time series instead of a scalar. Inputs are bounded by the
	// same promQueryRE allowlist as the instant query handler.
	mux.HandleFunc("GET /api/instances/{name}/metrics/range", handleMetricsRange())
}

// metricsTimeout caps how long the handler will wait on the
// Prometheus subprocess + HTTP request chain. 10s matches the
// CLI's per-call timeout.
const metricsTimeout = 10 * time.Second

// maxQueryLen caps PromQL input length before it ever reaches the
// regex matcher. Defence against catastrophic backtracking + a
// trivial sanity bound — the largest panel we ship is ~280 bytes.
const maxQueryLen = 4096

// promQueryRE pins the allowed character set for the `query`
// param — alphanumeric + PromQL operators + whitespace. Blocks
// shell metachars and request-smuggling attempts before we hand
// the string to net/url.QueryEscape (defence-in-depth: even
// though we URL-encode, refusing weird input early gives clearer
// 400s than a confusing Prometheus error).
var promQueryRE = regexp.MustCompile(`^[a-zA-Z0-9_{}\[\]:".,= \-+*/()<>!^]+$`)

func handleMetricsQuery() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		query := r.URL.Query().Get("query")
		if query == "" {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"missing query parameter",
				"pass ?query=<PromQL> — e.g. ?query=up")
			return
		}
		// Guard the regex from pathological inputs (review yellow): a
		// 4 KiB ceiling is generous for any panel we ship and stops
		// catastrophic-backtracking probes early.
		if len(query) > maxQueryLen {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"query exceeds maximum length",
				fmt.Sprintf("query must be <= %d bytes", maxQueryLen))
			return
		}
		if !promQueryRE.MatchString(query) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"query contains disallowed characters",
				"PromQL grammar only — alphanumeric + operators + braces")
			return
		}

		state, err := registry.Read(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"instance "+name+" not registered")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), metricsTimeout)
		defer cancel()
		body, err := proxyPrometheus(ctx, state.ComposeProject, "/api/v1/query?"+url.Values{"query": {query}}.Encode())
		if err != nil {
			if errors.Is(err, errPrometheusNotRunning) {
				writeErrorWithCode(w, http.StatusServiceUnavailable,
					"OBSERVABILITY_PROFILE_OFF",
					"observability profile not running for instance "+name,
					"restart the instance with `dpm localnet up --profile observability --name "+name+"`")
				return
			}
			writeErrorWithCode(w, http.StatusBadGateway,
				"PROMETHEUS_PROXY_FAILED",
				"failed to query prometheus",
				err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

// handleMetricsRange wraps Prometheus's range-query endpoint
// (/api/v1/query_range). Returned shape matches Prometheus's own
// response so the frontend can decode {data.result[].values[][t,v]}
// without an extra projection layer.
//
// Parameters:
//   - query    : PromQL expression (same allowlist as the instant
//                handler; promQueryRE)
//   - window   : duration string ("5m", "1h", "24h"); clamped to
//                [1m, 24h]
//   - step     : duration string ("10s", "1m"); clamped to [5s, 1h];
//                defaults to window/60 so a default window yields
//                ~60 samples (good chart resolution without
//                hammering Prometheus)
//
// Same OBSERVABILITY_PROFILE_OFF semantics as the summary endpoint:
// 503 with structured envelope when Prometheus isn't running for
// the project.
func handleMetricsRange() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		q := r.URL.Query().Get("query")
		if len(q) > maxQueryLen {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"query exceeds maximum length",
				fmt.Sprintf("query must be <= %d bytes", maxQueryLen))
			return
		}
		if q == "" || !promQueryRE.MatchString(q) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"missing or invalid query parameter",
				"pass ?query=<PromQL> using the standard PromQL grammar")
			return
		}
		windowStr := r.URL.Query().Get("window")
		if windowStr == "" {
			windowStr = "1h"
		}
		window, err := time.ParseDuration(windowStr)
		if err != nil || window < time.Minute || window > 24*time.Hour {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid window",
				"window must be a duration between 1m and 24h (e.g. 5m, 1h, 24h)")
			return
		}
		stepStr := r.URL.Query().Get("step")
		var step time.Duration
		if stepStr != "" {
			step, err = time.ParseDuration(stepStr)
			if err != nil || step < 5*time.Second || step > time.Hour {
				writeErrorWithCode(w, http.StatusBadRequest,
					ErrCodeInvalidRequest,
					"invalid step",
					"step must be a duration between 5s and 1h (e.g. 10s, 1m)")
				return
			}
		} else {
			step = window / 60
			if step < 5*time.Second {
				step = 5 * time.Second
			}
		}

		state, err := registry.Read(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"instance "+name+" not registered")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), metricsTimeout)
		defer cancel()

		now := time.Now()
		path := "/api/v1/query_range" +
			"?query=" + url.QueryEscape(q) +
			"&start=" + strconv.FormatInt(now.Add(-window).Unix(), 10) +
			"&end=" + strconv.FormatInt(now.Unix(), 10) +
			"&step=" + strconv.FormatFloat(step.Seconds(), 'f', -1, 64) + "s"

		body, err := proxyPrometheus(ctx, state.ComposeProject, path)
		if err != nil {
			if errors.Is(err, errPrometheusNotRunning) {
				writeErrorWithCode(w, http.StatusServiceUnavailable,
					"OBSERVABILITY_PROFILE_OFF",
					"observability profile not running for instance "+name,
					"restart the instance with `dpm localnet up --profile observability --name "+name+"`")
				return
			}
			writeError(w, http.StatusBadGateway, "prometheus query_range", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

// handleMetricsSummary returns the same headline summary the CLI's
// `dpm localnet metrics --format json` prints. Lets the Web UI
// render a status card without doing four round-trips for the four
// queries (and ensures the two surfaces always show the same set
// of headline numbers).
func handleMetricsSummary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		state, err := registry.Read(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"instance "+name+" not registered")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), metricsTimeout)
		defer cancel()

		// BIT-134 review v4: queries come from the shared
		// metricsq package so CLI + handler can't drift.
		out := map[string]*float64{}
		type res struct {
			k metricsq.Headline
			v *float64
		}
		ch := make(chan res, len(metricsq.SummaryQueries))
		for k, q := range metricsq.SummaryQueries {
			go func(k metricsq.Headline, q string) {
				v, _ := singleScalar(ctx, state.ComposeProject, q)
				ch <- res{k, v}
			}(k, q)
		}
		anyFound := false
		for range metricsq.SummaryQueries {
			r := <-ch
			if r.v != nil {
				out[string(r.k)] = r.v
				anyFound = true
			}
		}
		if !anyFound {
			// Best signal that observability isn't running — no
			// queries returned data. Surface the structured code
			// so the UI can offer the "enable profile" CTA.
			if _, err := proxyPrometheus(ctx, state.ComposeProject, "/api/v1/query?query=up"); errors.Is(err, errPrometheusNotRunning) {
				writeErrorWithCode(w, http.StatusServiceUnavailable,
					"OBSERVABILITY_PROFILE_OFF",
					"observability profile not running for instance "+name,
					"restart the instance with `dpm localnet up --profile observability --name "+name+"`")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": 1,
			"instance":       name,
			"metrics":        out,
		})
	}
}

// proxyPrometheus does the actual HTTP call against the
// per-instance prometheus container. Returns errPrometheusNotRunning
// when no prometheus is present in the project so the caller can
// map to the OBSERVABILITY_PROFILE_OFF code.
//
// Discovery: walks `compose ps` for a service named "prometheus".
// When found we hit it via 127.0.0.1:<host-port>.
//
// Defence (review blocker #6):
//   - dedicated http.Client with Timeout = metricsTimeout (no longer
//     leaks http.DefaultClient's unbounded behaviour on a misbehaving
//     upstream)
//   - response body bounded by io.LimitReader so a runaway Prometheus
//     cannot OOM the devkit. 16 MiB is well above the largest range
//     response we observe in practice (a 24h × 5s step × 50-series
//     scrape is ~6 MiB) but small enough to fail closed.
func proxyPrometheus(ctx context.Context, project, path string) ([]byte, error) {
	host, port, err := discoverPrometheus(ctx, project)
	if err != nil {
		return nil, err
	}
	u := "http://" + host + ":" + strconv.Itoa(port) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: metricsTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Cap body at 16 MiB + 1 so we can detect overrun.
	const maxBody = 16 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("prometheus response exceeded %d bytes", maxBody)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return body, nil
}

// discoverPrometheus resolves the per-instance Prometheus address.
// 9090 is the CONTAINER-internal port; the host port is whatever
// Docker ephemerally assigned, captured into state.Ports["prometheus_ui"]
// during `localnet up` (PR #20 stable-ports contract).
//
// We need a project → instance-name reverse lookup because containers.List
// is keyed by compose project, not by registry name. The convention
// is canton-<name>, codified at internal/localnet/up.go ~L315.
func discoverPrometheus(ctx context.Context, project string) (string, int, error) {
	infos, err := containers.List(ctx, project)
	if err != nil {
		return "", 0, err
	}
	running := false
	for _, c := range infos {
		if c.Service == "prometheus" {
			running = true
			break
		}
	}
	if !running {
		return "", 0, errPrometheusNotRunning
	}
	// project naming convention: "canton-<name>"
	name := strings.TrimPrefix(project, "canton-")
	st, err := registry.Read(name)
	if err != nil {
		return "", 0, fmt.Errorf("discover prometheus: %w", err)
	}
	port, ok := st.Ports["prometheus_ui"]
	if !ok || port == 0 {
		return "", 0, errPrometheusNotRunning
	}
	return "127.0.0.1", port, nil
}

var errPrometheusNotRunning = errors.New("prometheus container not present in compose project")

func singleScalar(ctx context.Context, project, query string) (*float64, error) {
	body, err := proxyPrometheus(ctx, project,
		"/api/v1/query?"+url.Values{"query": {query}}.Encode())
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			Result [][]any `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != "success" || len(parsed.Data.Result) == 0 {
		return nil, nil
	}
	entry := parsed.Data.Result[0]
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
	return &v, nil
}
