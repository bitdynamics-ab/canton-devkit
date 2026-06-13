package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestIdempotency_ReplaysCompletedResponse: a second POST with the same
// Idempotency-Key returns the cached response and does NOT re-run the
// handler (the mutation happens once).
func TestIdempotency_ReplaysCompletedResponse(t *testing.T) {
	var calls int32
	h := newIdemStore().wrap(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"call":` + strconv.Itoa(int(n)) + `}`))
	})

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/tokens/RTK/mint", nil)
		req.Header.Set("Idempotency-Key", "abc-123")
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}

	first := do()
	second := do()

	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1 (mutation must not repeat)", calls)
	}
	if first.Body.String() != second.Body.String() || first.Code != second.Code {
		t.Errorf("replay differs: first=%d %q second=%d %q",
			first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Error("replay should set Idempotency-Replayed: true")
	}
}

// TestIdempotency_OptOutWithoutHeader: no header → no dedup, handler runs
// every time.
func TestIdempotency_OptOutWithoutHeader(t *testing.T) {
	var calls int32
	h := newIdemStore().wrap(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNoContent)
	})
	for i := 0; i < 3; i++ {
		h(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/tokens", nil))
	}
	if calls != 3 {
		t.Fatalf("without a key the handler must run every time; ran %d", calls)
	}
}

// TestIdempotency_InFlightConflict: a duplicate key whose first request
// hasn't completed gets 409 (so a concurrent double-submit can't run the
// mutation twice).
func TestIdempotency_InFlightConflict(t *testing.T) {
	s := newIdemStore()
	// Seed an in-flight (not done) entry by hand.
	s.entries["POST /api/tokens/RTK/burn k1"] = &idemEntry{created: s.now()}

	h := s.wrap(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest("POST", "/api/tokens/RTK/burn", nil)
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("in-flight duplicate status = %d, want 409", rec.Code)
	}
}

// TestIdempotency_TTLEviction: past the TTL an entry is GC'd, so the key
// is treated as new again.
func TestIdempotency_TTLEviction(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newIdemStore()
	s.now = func() time.Time { return now }

	var calls int32
	h := s.wrap(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNoContent)
	})
	req := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/tokens/RTK/mint", nil)
		r.Header.Set("Idempotency-Key", "ttl-key")
		return r
	}
	h(httptest.NewRecorder(), req())            // first
	now = now.Add(idempotencyTTL + time.Minute) // advance past TTL
	h(httptest.NewRecorder(), req())            // entry evicted → runs again
	if calls != 2 {
		t.Fatalf("after TTL the key should run again; calls=%d", calls)
	}
}

// TestIdempotency_DoesNotCache5xx: a transient 5xx must not be cached
// and replayed for the TTL. The first attempt fails (503), the second
// request with the SAME key re-runs the handler (which now succeeds)
// rather than replaying the stale failure.
func TestIdempotency_DoesNotCache5xx(t *testing.T) {
	var calls int32
	h := newIdemStore().wrap(func(w http.ResponseWriter, _ *http.Request) {
		// First call fails with a transient 503; second succeeds.
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"UNAVAILABLE"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	do := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/tokens/RTK/mint", nil)
		r.Header.Set("Idempotency-Key", "retry-after-blip")
		rec := httptest.NewRecorder()
		h(rec, r)
		return rec
	}

	first := do()
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first attempt status = %d, want 503", first.Code)
	}
	second := do()
	if calls != 2 {
		t.Fatalf("handler ran %d times; a cached 5xx must not block the retry (want 2)", calls)
	}
	if second.Code != http.StatusNoContent {
		t.Errorf("retry status = %d, want 204 (re-attempt, not a replayed 503)", second.Code)
	}
	if second.Header().Get("Idempotency-Replayed") == "true" {
		t.Error("a 5xx must not be replayed")
	}
}

func TestSanitize400_MasksPartyFingerprint(t *testing.T) {
	cases := []struct{ in, wantContains, wantNotContains string }{
		{"party alice::1220abcdef0123456789 not found", "alice::…", "1220abcdef0123456789"},
		{"amount must be > 0", "amount must be > 0", ""},
	}
	for _, c := range cases {
		got := sanitize400(c.in)
		if c.wantContains != "" && !contains(got, c.wantContains) {
			t.Errorf("sanitize400(%q) = %q, want it to contain %q", c.in, got, c.wantContains)
		}
		if c.wantNotContains != "" && contains(got, c.wantNotContains) {
			t.Errorf("sanitize400(%q) = %q, leaked %q", c.in, got, c.wantNotContains)
		}
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
