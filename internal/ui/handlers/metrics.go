package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
}

// metricsTimeout caps how long the handler will wait on the
// Prometheus subprocess + HTTP request chain. 10s matches the
// CLI's per-call timeout.
const metricsTimeout = 10 * time.Second

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
// When found we hit it via 127.0.0.1:<host-port>. The default
// 9090 matches the compose overlay's host bind.
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return body, nil
}

func discoverPrometheus(ctx context.Context, project string) (string, int, error) {
	infos, err := containers.List(ctx, project)
	if err != nil {
		return "", 0, err
	}
	for _, c := range infos {
		if c.Service == "prometheus" {
			return "127.0.0.1", 9090, nil
		}
	}
	return "", 0, errPrometheusNotRunning
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
