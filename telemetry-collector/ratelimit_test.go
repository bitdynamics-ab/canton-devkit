package collector

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is the wrapped handler — returns 200 so tests can tell a
// pass-through (200) from a rate-limit block (429).
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func reqFrom(ip, path string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, nil)
	r.Header.Set("CF-Connecting-IP", ip)
	return r
}

func TestRateLimit_PerIPBurstThenBlock(t *testing.T) {
	// 60/min = 1/s sustained, burst 2: first 2 immediate requests pass,
	// the 3rd is blocked (bucket empty, no time to refill).
	h := RateLimit(okHandler, RateLimitConfig{PerIPPerMinute: 60, Burst: 2, GlobalPerSec: 1000})
	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, reqFrom("1.2.3.4", "/v1/counters"))
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 {
		t.Errorf("first two requests = %v, want 200,200", codes[:2])
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Errorf("3rd request = %d, want 429", codes[2])
	}
}

func TestRateLimit_DistinctIPsIndependent(t *testing.T) {
	h := RateLimit(okHandler, RateLimitConfig{PerIPPerMinute: 60, Burst: 1, GlobalPerSec: 1000})
	// IP A exhausts its single-token burst.
	recA1 := httptest.NewRecorder()
	h.ServeHTTP(recA1, reqFrom("10.0.0.1", "/v1/counters"))
	recA2 := httptest.NewRecorder()
	h.ServeHTTP(recA2, reqFrom("10.0.0.1", "/v1/counters"))
	if recA1.Code != 200 || recA2.Code != http.StatusTooManyRequests {
		t.Errorf("IP A = %d,%d, want 200,429", recA1.Code, recA2.Code)
	}
	// IP B is unaffected by A's exhaustion.
	recB := httptest.NewRecorder()
	h.ServeHTTP(recB, reqFrom("10.0.0.2", "/v1/counters"))
	if recB.Code != 200 {
		t.Errorf("IP B = %d, want 200 (independent bucket)", recB.Code)
	}
}

func TestRateLimit_GlobalCeiling(t *testing.T) {
	// Global ceiling of 1/s (burst 1): the 2nd request — even from a
	// different IP with plenty of per-IP budget — is blocked.
	h := RateLimit(okHandler, RateLimitConfig{PerIPPerMinute: 10000, Burst: 1000, GlobalPerSec: 1})
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, reqFrom("1.1.1.1", "/v1/counters"))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, reqFrom("2.2.2.2", "/v1/counters"))
	if rec1.Code != 200 {
		t.Errorf("1st = %d, want 200", rec1.Code)
	}
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("2nd (global cap) = %d, want 429", rec2.Code)
	}
}

func TestRateLimit_HealthzExempt(t *testing.T) {
	// Even a global ceiling of 1 must never throttle /healthz.
	h := RateLimit(okHandler, RateLimitConfig{PerIPPerMinute: 1, Burst: 1, GlobalPerSec: 1})
	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, reqFrom("9.9.9.9", "/healthz"))
		if rec.Code != 200 {
			t.Fatalf("healthz probe %d = %d, want 200 (never limited)", i, rec.Code)
		}
	}
}

func TestClientIP_HeaderPrecedence(t *testing.T) {
	cases := []struct {
		name            string
		cf, xff, remote string
		want            string
	}{
		{"cf wins", "203.0.113.5", "198.51.100.1, 10.0.0.1", "192.0.2.9:443", "203.0.113.5"},
		{"xff first hop", "", "198.51.100.1, 10.0.0.1", "192.0.2.9:443", "198.51.100.1"},
		{"remote fallback", "", "", "192.0.2.9:443", "192.0.2.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/counters", nil)
			r.RemoteAddr = tc.remote
			if tc.cf != "" {
				r.Header.Set("CF-Connecting-IP", tc.cf)
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
