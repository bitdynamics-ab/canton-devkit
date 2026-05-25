package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server wraps a configured *http.Server with lifecycle plumbing the CLI
// command (internal/cli/localnet/ui.go) drives.
//
// Three deliberate design choices, each load-bearing:
//
//  1. Bind strictly to 127.0.0.1 (loopback). Never 0.0.0.0. The Web UI
//     reads JWTs and party identifiers; exposing it on the network
//     would broadcast credentials to anyone on the LAN. There is NO
//     CONFIG to widen this — if a user needs remote access they SSH-
//     tunnel. Documented in the godoc on Config.Host.
//
//  2. ReadHeaderTimeout is set (slowloris defence). Go's default
//     http.Server has no timeouts, which means a malicious client
//     can dribble headers byte-by-byte forever and pin a goroutine.
//     Even on loopback this matters — a browser tab leak can do the
//     same accidentally.
//
//  3. Listener is created BEFORE the HTTP serve loop starts so the
//     CLI can print the URL with the actual bound port (Config.Port=0
//     → OS-assigned) and the readiness signal carries the actual
//     address. Decoupling Listen() from Serve() is what makes
//     `dpm localnet ui` reliably print "Open http://127.0.0.1:7777"
//     even when 7777 was already taken.
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
	// Port is the TCP port; 0 means OS-assigned (tested by Web UI
	// to find a free port without racing). Default 7777, matching
	// the JSX mockup URL bar.
	Port int
	// Router is the http.Handler tree (typically built by NewRouter).
	// Required.
	Router http.Handler
}

// defaults applied by New when zero. Kept as a function (not const) so
// tests can swap by passing a fully-populated Config without ceremony.
//
// Port intentionally has NO default substitution: 0 is a valid value
// meaning "OS-assigned" (used by `--port 0`, tests, and CI smoke
// checks). The CLI flag in internal/cli/localnet/ui.go owns the 7777
// human-default; pushing it here would prevent legitimate Port=0
// callers from getting OS-assigned ports.
func (c Config) withDefaults() Config {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	return c
}

// New constructs the Server. Does NOT bind a listener — call Listen()
// for that. Two-step construction is deliberate: tests can construct
// a Server, inspect Config, then Listen on an OS-assigned port to
// avoid clashing with another local test.
func New(cfg Config) *Server {
	cfg = cfg.withDefaults()
	return &Server{
		cfg: cfg,
		http: &http.Server{
			Handler:           cfg.Router,
			ReadHeaderTimeout: 10 * time.Second, // slowloris floor
		},
	}
}

// Listen binds the configured address and returns the resolved listener
// address (host:port). Call BEFORE Serve so a caller can print the URL
// before the blocking Serve loop starts.
//
// Returns the resolved addr (matters when Port=0).
func (s *Server) Listen() (string, error) {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.ln = ln
	s.addr = ln.Addr().String()
	return s.addr, nil
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

// Shutdown gracefully stops the server: stops accepting new connections,
// waits up to `ctx` deadline for in-flight requests to finish, then
// returns. The caller decides the deadline — `dpm localnet ui` uses
// 5s, which is long enough for a single API request but short enough
// that ^C feels responsive.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}
