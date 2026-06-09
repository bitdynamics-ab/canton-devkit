package collector

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig tunes the in-process limiter. Zero fields take the
// defaults in withDefaults.
//
// This limiter is DEFENSE-IN-DEPTH, not the primary DDoS control. Heavy
// volumetric absorption belongs at the network edge — run the collector
// behind a Cloudflare Tunnel (see DEPLOY.md) so floods never reach this
// process. These limits exist to (a) protect Postgres from write
// amplification and (b) bound abuse that slips past the edge.
type RateLimitConfig struct {
	PerIPPerMinute int           // sustained requests/min per client IP
	Burst          int           // per-IP burst allowance
	GlobalPerSec   int           // ceiling on total accepted req/s across all IPs
	IdleTTL        time.Duration // forget a per-IP bucket after this idle time
	MaxTrackedIPs  int           // hard cap on tracked IPs (bounds memory)
}

func (c RateLimitConfig) withDefaults() RateLimitConfig {
	if c.PerIPPerMinute <= 0 {
		c.PerIPPerMinute = 30 // a real client POSTs ~1/day; 30/min is generous
	}
	if c.Burst <= 0 {
		c.Burst = 15
	}
	if c.GlobalPerSec <= 0 {
		c.GlobalPerSec = 50
	}
	if c.IdleTTL <= 0 {
		c.IdleTTL = 15 * time.Minute
	}
	if c.MaxTrackedIPs <= 0 {
		c.MaxTrackedIPs = 100_000
	}
	return c
}

// RateLimitConfigFromEnv reads the limiter knobs from the environment,
// falling back to defaults so an unset deployment still gets protection.
//
//	RATE_PER_IP_PER_MIN   per-IP sustained rate (default 30)
//	RATE_BURST            per-IP burst (default 15)
//	RATE_GLOBAL_PER_SEC   global ceiling req/s (default 50)
func RateLimitConfigFromEnv() RateLimitConfig {
	atoi := func(k string) int { n, _ := strconv.Atoi(os.Getenv(k)); return n }
	return RateLimitConfig{
		PerIPPerMinute: atoi("RATE_PER_IP_PER_MIN"),
		Burst:          atoi("RATE_BURST"),
		GlobalPerSec:   atoi("RATE_GLOBAL_PER_SEC"),
	}.withDefaults()
}

type ipBucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

type rateLimiter struct {
	next   http.Handler
	cfg    RateLimitConfig
	perIP  rate.Limit
	global *rate.Limiter
	nowFn  func() time.Time // test seam

	mu        sync.Mutex
	buckets   map[string]*ipBucket
	lastSweep time.Time
}

// RateLimit wraps next with per-IP and global token-bucket limiting.
// Exceeding either returns 429 with a Retry-After header. /healthz is
// always exempt so orchestrator liveness probes are never throttled.
func RateLimit(next http.Handler, cfg RateLimitConfig) http.Handler {
	cfg = cfg.withDefaults()
	rl := &rateLimiter{
		next:    next,
		cfg:     cfg,
		perIP:   rate.Limit(float64(cfg.PerIPPerMinute) / 60.0),
		global:  rate.NewLimiter(rate.Limit(cfg.GlobalPerSec), cfg.GlobalPerSec),
		nowFn:   time.Now,
		buckets: make(map[string]*ipBucket),
	}
	rl.lastSweep = rl.nowFn()
	return rl
}

func (rl *rateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" { // liveness probes are never limited
		rl.next.ServeHTTP(w, r)
		return
	}
	// Global ceiling first — protects the store regardless of how the
	// load is spread across source IPs.
	if !rl.global.Allow() {
		tooMany(w)
		return
	}
	if !rl.allowIP(clientIP(r)) {
		tooMany(w)
		return
	}
	rl.next.ServeHTTP(w, r)
}

func (rl *rateLimiter) allowIP(ip string) bool {
	now := rl.nowFn()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Piggy-backed periodic sweep of idle buckets — at most once per
	// IdleTTL, amortized across requests, so there's no background
	// goroutine and steady traffic keeps the map bounded.
	if now.Sub(rl.lastSweep) > rl.cfg.IdleTTL {
		for k, b := range rl.buckets {
			if now.Sub(b.lastSeen) > rl.cfg.IdleTTL {
				delete(rl.buckets, k)
			}
		}
		rl.lastSweep = now
	}

	b, ok := rl.buckets[ip]
	if !ok {
		if len(rl.buckets) >= rl.cfg.MaxTrackedIPs {
			// Map is full (likely a unique-IP flood). Don't grow it
			// unbounded — the request already passed the global ceiling,
			// so let it through and rely on that ceiling.
			return true
		}
		b = &ipBucket{lim: rate.NewLimiter(rl.perIP, rl.cfg.Burst)}
		rl.buckets[ip] = b
	}
	b.lastSeen = now
	return b.lim.Allow()
}

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	http.Error(w, "rate limited", http.StatusTooManyRequests)
}

// clientIP resolves the originating client IP. Behind a Cloudflare Tunnel
// the real client is in CF-Connecting-IP; a generic reverse proxy sets
// X-Forwarded-For (we take the first hop). Falls back to the transport
// peer address.
//
// SECURITY: these headers are trustworthy ONLY when the collector runs
// behind a proxy that sets them (the documented deployment). Do not
// expose the collector's port directly to the internet, or a client can
// spoof CF-Connecting-IP to dodge per-IP limits.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
