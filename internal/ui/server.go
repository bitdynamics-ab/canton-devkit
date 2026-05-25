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
// a non-loopback IP and AllowNonLoopback is false. Reviewer pin
// (PR #41 #a): the previous shape merely "strongly discouraged"
// non-loopback in --host's help text, then bound anyway. Defence-in-
// depth requires a real refusal at the bind layer — the docstring
// is not load-bearing security.
var ErrNonLoopbackBind = errors.New("ui server: refusing non-loopback bind without AllowNonLoopback")

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
	// AllowNonLoopback opts the caller into binding on a non-loopback
	// interface. When false (default), Listen() refuses any Host
	// that resolves to anything other than a loopback IP with
	// ErrNonLoopbackBind. The CLI exposes this as an explicit flag
	// (`--allow-non-loopback`) that defaults off; users who genuinely
	// need LAN binding have to type the flag.
	//
	// Reviewer pin (PR #41 #a): the docstring's "loopback only"
	// claim was advisory until this gate landed. The default-deny
	// posture means a future regression that silently widens the
	// bind fails closed.
	AllowNonLoopback bool
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
//
// Timeouts (load-bearing — see Server docstring):
//
//   - ReadHeaderTimeout (10s): slowloris floor, prevents byte-dribble
//     attacks on header parsing.
//   - IdleTimeout (60s): closes connections idle on keep-alive. Without
//     this, a browser tab that goes to sleep can pin a server-side
//     conn (and goroutine) indefinitely; over hours of use that
//     accumulates into a small leak. 60s matches the typical proxy
//     idle window AND is twice the SSE heartbeat (30s in sse.go),
//     so a healthy SSE stream's keepalives reset the timer well
//     before it fires. Reviewer pin (PR #41 #1).
func New(cfg Config) *Server {
	cfg = cfg.withDefaults()
	return &Server{
		cfg: cfg,
		http: &http.Server{
			Handler:           cfg.Router,
			ReadHeaderTimeout: 10 * time.Second, // slowloris floor
			IdleTimeout:       60 * time.Second, // keep-alive reap
		},
	}
}

// Listen binds the configured address and returns the resolved listener
// address (host:port). Call BEFORE Serve so a caller can print the URL
// before the blocking Serve loop starts.
//
// Returns the resolved addr (matters when Port=0).
//
// Refuses non-loopback Host values unless Config.AllowNonLoopback is
// true. The check resolves Host via net.LookupIP so a hostname like
// "0.0.0.0" or "localhost.example.com" is gated correctly, not just
// literal IP strings. See ErrNonLoopbackBind.
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
// Treats "0.0.0.0" / "::" explicitly as non-loopback (they're INADDR_ANY
// — bind on ALL interfaces, which is the worst case for "loopback
// only"). Empty host is treated as loopback because the withDefaults
// upgrade fills it to 127.0.0.1; reaching here with "" would be a
// programming bug, but we tolerate it as loopback rather than crashing.
func assertLoopback(host string) error {
	if host == "" {
		return nil
	}
	// Reject INADDR_ANY explicitly — these resolve to "any" not "loopback".
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
