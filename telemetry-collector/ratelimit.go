package collector

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	// TrustedIPHeader names the request header that carries the real
	// client IP, set by the proxy the collector runs behind. EMPTY (the
	// default) means "trust nothing" → key per-IP limits off the transport
	// peer address. This is the safe default: a client can forge any
	// header, so we only honor one when the operator explicitly declares
	// their topology. Set it to:
	//   - "CF-Connecting-IP"  behind a Cloudflare Tunnel (CF overwrites it,
	//                         so it can't be spoofed)
	//   - "X-Forwarded-For"   behind a single appending proxy (Caddy/nginx);
	//                         we take the LAST hop (the IP your proxy
	//                         appended), which a client can't forge.
	TrustedIPHeader string
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
//	TRUSTED_IP_HEADER     header carrying the real client IP, e.g.
//	                      "CF-Connecting-IP" (CF Tunnel) or
//	                      "X-Forwarded-For" (Caddy/nginx). Unset = trust
//	                      none (use the peer address) — the safe default.
func RateLimitConfigFromEnv() RateLimitConfig {
	atoi := func(k string) int { n, _ := strconv.Atoi(os.Getenv(k)); return n }
	return RateLimitConfig{
		PerIPPerMinute:  atoi("RATE_PER_IP_PER_MIN"),
		Burst:           atoi("RATE_BURST"),
		GlobalPerSec:    atoi("RATE_GLOBAL_PER_SEC"),
		TrustedIPHeader: strings.TrimSpace(os.Getenv("TRUSTED_IP_HEADER")),
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

	blocked atomic.Uint64 // cumulative 429s, for sampled logging

	mu        sync.Mutex
	buckets   map[string]*ipBucket
	lastSweep time.Time
}

// rejectLogSample logs every Nth rejection so an attack is visible in the
// logs without flooding them under sustained abuse.
const rejectLogSample = 100

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
	if isHealthz(r.URL.Path) { // liveness probes are never limited
		rl.next.ServeHTTP(w, r)
		return
	}
	ip := clientIP(r, rl.cfg.TrustedIPHeader)
	// Global ceiling first — protects the store regardless of how the
	// load is spread across source IPs.
	if !rl.global.Allow() {
		rl.reject(w, ip, "global")
		return
	}
	if !rl.allowIP(ip) {
		rl.reject(w, ip, "per-ip")
		return
	}
	rl.next.ServeHTTP(w, r)
}

func isHealthz(p string) bool { return p == "/healthz" || p == "/healthz/" }

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

func (rl *rateLimiter) reject(w http.ResponseWriter, ip, reason string) {
	// Sampled log so an attack is visible without flooding the log under
	// sustained abuse. The 1st, 101st, 201st… rejection is logged.
	if n := rl.blocked.Add(1); n%rejectLogSample == 1 {
		log.Printf("collector: rate limited ip=%s reason=%s (sampled 1/%d, cumulative=%d)",
			ip, reason, rejectLogSample, n)
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "rate limited", http.StatusTooManyRequests)
}

// clientIP resolves the per-IP-limit key.
//
// When trustedHeader is empty (the default), the transport peer address
// is used — a client can forge any header, so we trust none by default.
// When the operator declares a header (matching the proxy in front), the
// LAST hop in that header is used: a single appending proxy puts the real
// connecting client there, and a client can't forge entries appended
// AFTER its own. The value is IP-validated; anything malformed falls back
// to the peer address (so a misconfigured upstream can't create junk
// bucket keys). See RateLimitConfig.TrustedIPHeader.
func clientIP(r *http.Request, trustedHeader string) string {
	if trustedHeader != "" {
		if v := r.Header.Get(trustedHeader); v != "" {
			parts := strings.Split(v, ",")
			cand := strings.TrimSpace(parts[len(parts)-1]) // last hop
			if ip := net.ParseIP(cand); ip != nil {
				return ip.String()
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
