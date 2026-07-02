package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrNonLoopbackBind is returned by Listen when Config.Host resolves to
// a non-loopback IP and AllowNonLoopback is false: the loopback-only
// posture is enforced at the bind layer, not just documented.
var ErrNonLoopbackBind = errors.New("ui server: refusing non-loopback bind without AllowNonLoopback")

// Server wraps a configured *http.Server with the lifecycle plumbing the
// CLI command drives. Three load-bearing design choices:
//
//  1. Binds to loopback by default. The Web UI reads JWTs and party
//     identifiers; exposing it on the network would broadcast
//     credentials to the LAN. Non-loopback binds require the explicit
//     AllowNonLoopback opt-in.
//
//  2. ReadHeaderTimeout is set (slowloris defence). Go's default
//     http.Server has no timeouts, so a client dribbling headers
//     byte-by-byte pins a goroutine forever — even on loopback a leaky
//     browser tab can do this accidentally.
//
//  3. The listener is created before the serve loop (Listen vs Serve)
//     so the CLI can print the URL with the actual bound port
//     (Config.Port=0 → OS-assigned) before blocking.
type Server struct {
	cfg  Config
	http *http.Server
	ln   net.Listener
	addr string // resolved listener addr; populated by Listen
}

// Config is the public knob set for Server. All fields have sane defaults
// so a caller can `New(Config{})` for the common case.
type Config struct {
	// Host is the bind address. Loopback only — see the Server
	// docstring. Default 127.0.0.1.
	Host string
	// Port is the TCP port; 0 means OS-assigned. The CLI flag owns
	// the 7777 human default.
	Port int
	// Router is the http.Handler tree (typically built by NewRouter).
	// Required.
	Router http.Handler
	// AllowNonLoopback opts the caller into binding on a non-loopback
	// interface. When false (default), Listen() refuses any Host that
	// resolves to a non-loopback IP with ErrNonLoopbackBind. The CLI
	// exposes this as `--allow-non-loopback` (default off), so a
	// regression that silently widens the bind fails closed.
	AllowNonLoopback bool
}

// withDefaults fills zero-value fields. Port deliberately has NO
// default substitution: 0 is a valid value meaning "OS-assigned" (used
// by `--port 0`, tests, and CI smoke checks); the CLI flag owns the
// 7777 human default.
func (c Config) withDefaults() Config {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	return c
}

// New constructs the Server. Does NOT bind a listener — call Listen()
// for that; two-step construction lets tests Listen on an OS-assigned
// port without racing other tests.
//
// Timeouts (load-bearing — see Server docstring):
//
//   - ReadHeaderTimeout (10s): slowloris floor.
//   - IdleTimeout (60s): reaps idle keep-alive connections that would
//     otherwise pin goroutines (e.g. sleeping browser tabs). Twice the
//     SSE heartbeat (30s in sse.go), so a healthy SSE stream's
//     keepalives reset the timer well before it fires.
func New(cfg Config) *Server {
	cfg = cfg.withDefaults()
	return &Server{
		cfg: cfg,
		http: &http.Server{
			Handler:           cfg.Router,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
}

// Listen binds the configured address and returns the resolved listener
// address (host:port — meaningful when Port=0). Call BEFORE Serve so a
// caller can print the URL before the blocking Serve loop starts.
//
// Refuses non-loopback Host values unless Config.AllowNonLoopback is
// true. The check resolves Host via net.LookupIP so hostnames are gated
// correctly, not just literal IP strings. See ErrNonLoopbackBind.
func (s *Server) Listen() (string, error) {
	if !s.cfg.AllowNonLoopback {
		if err := assertLoopback(s.cfg.Host); err != nil {
			return "", err
		}
	}
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.ln = ln
	s.addr = ln.Addr().String()
	return s.addr, nil
}

// assertLoopback resolves host and refuses any IP that isn't loopback.
// "0.0.0.0" / "::" are explicitly rejected as the wildcard bind (all
// interfaces — the worst case for "loopback only"). Empty host is
// tolerated as loopback: withDefaults fills it to 127.0.0.1 before this
// can run.
func assertLoopback(host string) error {
	if host == "" {
		return nil
	}
	if host == "0.0.0.0" || host == "::" || host == "[::]" {
		return fmt.Errorf("%w: %q is the wildcard bind, not loopback",
			ErrNonLoopbackBind, host)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve host %q: %v",
			ErrNonLoopbackBind, host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: host %q resolved to no IPs",
			ErrNonLoopbackBind, host)
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return fmt.Errorf("%w: host %q resolves to %s",
				ErrNonLoopbackBind, host, ip)
		}
	}
	return nil
}

// Addr returns the resolved listener address. Empty until Listen()
// succeeds. Useful for tests that need the actual port.
func (s *Server) Addr() string { return s.addr }

// Serve blocks on the HTTP loop. Call after Listen. Returns
// http.ErrServerClosed (nil-ish) when Shutdown is called; any other
// error is a genuine failure.
func (s *Server) Serve() error {
	if s.ln == nil {
		return errors.New("ui server: Serve called before Listen")
	}
	if err := s.http.Serve(s.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server: stops accepting new connections
// and waits up to the ctx deadline for in-flight requests to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}
