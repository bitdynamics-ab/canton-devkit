package collector

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeStore records the last upsert so tests can assert what the handler
// parsed and forwarded, and can be told to fail.
type fakeStore struct {
	period      string
	granularity string
	start       *time.Time
	counters    map[string]map[string]int
	calls       int
	failWith    error

	installID    string // last token passed to RecordInstall
	installCalls int
	installFail  error
}

func (f *fakeStore) UpsertCounters(_ context.Context, period, gran string, start *time.Time, counters map[string]map[string]int) error {
	f.calls++
	if f.failWith != nil {
		return f.failWith
	}
	f.period, f.granularity, f.start, f.counters = period, gran, start, counters
	return nil
}

func (f *fakeStore) RecordInstall(_ context.Context, installID, _ string, _ *time.Time) error {
	f.installCalls++
	if f.installFail != nil {
		return f.installFail
	}
	f.installID = installID
	return nil
}

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/counters", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const dailyBody = `{"schema_version":2,"period":"2026-06-04","granularity":"daily","counters":{"dpm/command":{"up":5,"down":2}}}`

func TestIngest_DailyHappyPath(t *testing.T) {
	fs := &fakeStore{}
	rec := post(New(fs, ""), dailyBody)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if fs.period != "2026-06-04" || fs.granularity != "daily" {
		t.Errorf("stored period/gran = %q/%q", fs.period, fs.granularity)
	}
	if fs.counters["dpm/command"]["up"] != 5 || fs.counters["dpm/command"]["down"] != 2 {
		t.Errorf("counters wrong: %+v", fs.counters)
	}
	// Daily period_date is the date itself.
	if fs.start == nil || fs.start.Format("2006-01-02") != "2026-06-04" {
		t.Errorf("period_date = %v, want 2026-06-04", fs.start)
	}
}

func TestIngest_WeeklyKeyComputesMonday(t *testing.T) {
	fs := &fakeStore{}
	// 2026-W23: ISO week 23 of 2026 starts Monday 2026-06-01.
	body := `{"schema_version":2,"period":"2026-W23","granularity":"weekly","counters":{"dpm/os":{"linux":3}}}`
	rec := post(New(fs, ""), body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if fs.start == nil || fs.start.Weekday() != time.Monday {
		t.Errorf("weekly period_date not a Monday: %v", fs.start)
	}
	if got := fs.start.Format("2006-01-02"); got != "2026-06-01" {
		t.Errorf("ISO week start = %s, want 2026-06-01", got)
	}
}

func TestIngest_LegacyWeekFieldFallback(t *testing.T) {
	fs := &fakeStore{}
	// v1 client: no "period"/"granularity", only "week".
	body := `{"schema_version":1,"week":"2026-W23","counters":{"dpm/command":{"up":1}}}`
	rec := post(New(fs, ""), body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if fs.period != "2026-W23" || fs.granularity != "weekly" {
		t.Errorf("legacy fallback: period/gran = %q/%q, want 2026-W23/weekly", fs.period, fs.granularity)
	}
}

func TestIngest_InstallIDRecorded(t *testing.T) {
	fs := &fakeStore{}
	const id = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	body := `{"schema_version":2,"period":"2026-06-04","granularity":"daily",` +
		`"install_id":"` + id + `","counters":{"dpm/command":{"up":1}}}`
	rec := post(New(fs, ""), body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if fs.installCalls != 1 || fs.installID != id {
		t.Errorf("RecordInstall calls=%d id=%q, want 1/%q", fs.installCalls, fs.installID, id)
	}
	// Counters still ingested alongside the dedup record.
	if fs.counters["dpm/command"]["up"] != 1 {
		t.Errorf("counters not ingested: %+v", fs.counters)
	}
}

func TestIngest_InstallIDOptional(t *testing.T) {
	fs := &fakeStore{}
	// No install_id (CI / old client) → counters ingest, no dedup call.
	if rec := post(New(fs, ""), dailyBody); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if fs.installCalls != 0 {
		t.Errorf("RecordInstall called %d times for id-less body, want 0", fs.installCalls)
	}
}

func TestIngest_InstallIDFailureDoesNotFailIngest(t *testing.T) {
	// The counters already committed; a dedup write error must not 500.
	fs := &fakeStore{installFail: context.DeadlineExceeded}
	body := `{"schema_version":2,"period":"2026-06-04","granularity":"daily",` +
		`"install_id":"3f2504e0-4f89-41d3-9a0c-0305e82c3301","counters":{"dpm/command":{"up":1}}}`
	if rec := post(New(fs, ""), body); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (dedup failure swallowed)", rec.Code)
	}
}

func TestIngest_Rejects(t *testing.T) {
	cases := map[string]struct {
		method, body string
		want         int
	}{
		"GET not allowed":   {http.MethodGet, "", http.StatusMethodNotAllowed},
		"bad json":          {http.MethodPost, `{`, http.StatusBadRequest},
		"unknown field":     {http.MethodPost, `{"period":"2026-06-04","granularity":"daily","counters":{"x":{"y":1}},"evil":1}`, http.StatusBadRequest},
		"bad period":        {http.MethodPost, `{"period":"notadate","granularity":"daily","counters":{"x":{"y":1}}}`, http.StatusBadRequest},
		"bad granularity":   {http.MethodPost, `{"period":"2026-06-04","granularity":"hourly","counters":{"x":{"y":1}}}`, http.StatusBadRequest},
		"empty counters":    {http.MethodPost, `{"period":"2026-06-04","granularity":"daily","counters":{}}`, http.StatusBadRequest},
		"negative count":    {http.MethodPost, `{"period":"2026-06-04","granularity":"daily","counters":{"x":{"y":-1}}}`, http.StatusBadRequest},
		"empty bucket name": {http.MethodPost, `{"period":"2026-06-04","granularity":"daily","counters":{"x":{"":1}}}`, http.StatusBadRequest},
		"bad install_id":    {http.MethodPost, `{"period":"2026-06-04","granularity":"daily","install_id":"not-a-uuid","counters":{"x":{"y":1}}}`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/v1/counters", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			New(&fakeStore{}, "").ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestIngest_TokenAuth(t *testing.T) {
	h := New(&fakeStore{}, "s3cret")
	// Missing token → 401.
	if rec := post(h, dailyBody); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec.Code)
	}
	// Correct token → 204.
	req := httptest.NewRequest(http.MethodPost, "/v1/counters", strings.NewReader(dailyBody))
	req.Header.Set("X-Telemetry-Token", "s3cret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("with token: status = %d, want 204", rec.Code)
	}
}

func TestIngest_StorageErrorIs500AndNoLeak(t *testing.T) {
	fs := &fakeStore{failWith: context.DeadlineExceeded}
	rec := post(New(fs, ""), dailyBody)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "context deadline") {
		t.Error("raw storage error leaked to client")
	}
}

func TestIngest_BodyCap(t *testing.T) {
	// > 1 MiB body must be rejected, not OOM the process.
	var b bytes.Buffer
	b.WriteString(`{"period":"2026-06-04","granularity":"daily","counters":{"dpm/command":{`)
	for i := 0; i < 200000; i++ {
		b.WriteString(`"x":1,`)
	}
	b.WriteString(`"y":1}}}`)
	rec := post(New(&fakeStore{}, ""), b.String())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body: status = %d, want 400", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	New(&fakeStore{}, "").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", rec.Code)
	}
}
