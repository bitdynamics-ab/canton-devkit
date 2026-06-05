// Package collector is the ingestion endpoint for canton-devkit usage
// telemetry. It accepts the CLI's per-period counter payload
// ({schema_version, period, granularity, counters}) over HTTP POST and
// upserts it into a relational store (Postgres in production, a fake in
// tests). It is operationally separate from the CLI — a server-side
// service you self-host — so it lives in its own Go module and its
// Postgres driver never enters the CLI's dependency graph.
//
// Zero-PII contract: the payload carries only counters keyed by
// chart/bucket plus a coarse period string. The collector stores exactly
// that and adds nothing. It must never log request bodies at info level
// or persist anything resembling an identifier.
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"time"
)

// maxBodyBytes caps a single ingest request. A real payload is a few KiB
// (≤10 counter charts × small bucket sets); anything past 1 MiB is abuse
// or a bug.
const maxBodyBytes = 1 << 20

// Payload is the wire shape the CLI uploader POSTs. Field names match
// internal/telemetry's uploadPeriod body. The legacy v1 "week" key is
// accepted as a fallback for Period so a mixed fleet mid-rollout still
// ingests.
type Payload struct {
	SchemaVersion int                       `json:"schema_version"`
	Period        string                    `json:"period"`
	LegacyWeek    string                    `json:"week"` // v1 fallback
	Granularity   string                    `json:"granularity"`
	Counters      map[string]map[string]int `json:"counters"`
}

// Store persists a validated period of counters. Implemented by pgxStore
// against Postgres; faked in tests. Aggregation semantics: many machines
// report the same period (zero-PII, no machine id), so submissions SUM on
// (period, chart, bucket) rather than replacing — see PgStore.UpsertCounters.
type Store interface {
	UpsertCounters(ctx context.Context, period, granularity string, periodStart *time.Time, counters map[string]map[string]int) error
}

// Handler is the HTTP ingest handler. Construct with New.
type Handler struct {
	store Store
	// token, when non-empty, requires an exact match in the
	// X-Telemetry-Token header. Defence for when the collector is exposed
	// beyond loopback. Empty disables the check (loopback/VPN default).
	token string
}

// New builds an ingest Handler. token may be "" to disable auth.
func New(store Store, token string) *Handler { return &Handler{store: store, token: token} }

// periodKeyRe constrains the period string to the two shapes the CLI
// emits — a calendar date "2026-06-04" or an ISO year-week "2026-W22".
// This is both a sanity gate and an injection guard (the value reaches a
// parameterized query, but we reject garbage early regardless).
var periodKeyRe = regexp.MustCompile(`^\d{4}-(\d{2}-\d{2}|W\d{2})$`)

var validGranularity = map[string]bool{"daily": true, "weekly": true}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.token != "" && r.Header.Get("X-Telemetry-Token") != h.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	p, err := decode(r)
	if err != nil {
		// 400s carry a terse reason (never the body) so a misconfigured
		// client can self-diagnose without us echoing arbitrary input.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	start := periodStart(p.Period, p.Granularity)
	if err := h.store.UpsertCounters(ctx, p.Period, p.Granularity, start, p.Counters); err != nil {
		// Never leak the DB error (DSN, table internals) to the client.
		log.Printf("ingest: upsert period=%s granularity=%s: %v", p.Period, p.Granularity, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decode reads, size-caps, and validates the request body into a Payload.
func decode(r *http.Request) (*Payload, error) {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	var p Payload
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid json body")
	}
	if p.Period == "" {
		p.Period = p.LegacyWeek // v1 fallback
	}
	if !periodKeyRe.MatchString(p.Period) {
		return nil, fmt.Errorf("period must be YYYY-MM-DD or YYYY-Www")
	}
	if p.Granularity == "" {
		// Infer from the key shape when an old client omits it.
		if len(p.Period) == 10 {
			p.Granularity = "daily"
		} else {
			p.Granularity = "weekly"
		}
	}
	if !validGranularity[p.Granularity] {
		return nil, fmt.Errorf("granularity must be daily or weekly")
	}
	if len(p.Counters) == 0 {
		return nil, fmt.Errorf("counters must be non-empty")
	}
	for chart, buckets := range p.Counters {
		if chart == "" || len(buckets) == 0 {
			return nil, fmt.Errorf("counter chart must be named and non-empty")
		}
		for bucket, n := range buckets {
			if bucket == "" {
				return nil, fmt.Errorf("counter bucket must be named")
			}
			if n < 0 {
				return nil, fmt.Errorf("counter value must be non-negative")
			}
		}
	}
	return &p, nil
}

// periodStart maps a period key to the date its bucket begins on, so the
// store can expose a real DATE column for time-series charts regardless
// of granularity. Daily → the date itself; weekly → the Monday of that
// ISO week. Returns nil when the key can't be parsed (the raw key is
// still stored; only the convenience date is omitted).
func periodStart(period, granularity string) *time.Time {
	if granularity == "weekly" || (len(period) == 8 && period[5] == 'W') {
		var y, wk int
		if _, err := fmt.Sscanf(period, "%4d-W%2d", &y, &wk); err != nil {
			return nil
		}
		t := isoWeekStart(y, wk)
		return &t
	}
	t, err := time.Parse("2006-01-02", period)
	if err != nil {
		return nil
	}
	return &t
}

// isoWeekStart returns the UTC Monday that begins ISO week `week` of
// `year`. ISO-8601: week 1 is the week containing the year's first
// Thursday; weeks start on Monday.
func isoWeekStart(year, week int) time.Time {
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC) // always in week 1
	// Monday of week 1.
	offset := int(time.Monday - jan4.Weekday())
	if offset > 0 {
		offset -= 7
	}
	week1Mon := jan4.AddDate(0, 0, offset)
	return week1Mon.AddDate(0, 0, (week-1)*7)
}

// ErrNoStore is returned by New-less zero values; guards a programming
// error rather than a runtime condition.
var ErrNoStore = errors.New("collector: nil store")
