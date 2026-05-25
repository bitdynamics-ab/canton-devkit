package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
)

// safeBuffer wraps bytes.Buffer with a mutex so the test goroutine
// reading the buffer doesn't race with the command goroutine writing
// to it. bytes.Buffer is not goroutine-safe by itself, and the
// `go test -race` detector correctly flagged this when the polling
// loop read out.String() while Execute() was still writing.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestUI_BindsAndShutdownsCleanly is the integration smoke for the
// `dpm localnet ui` command. We drive the command with --port 0 (to
// avoid colliding with any local dev server), parse the printed URL
// from stdout, hit /healthz, then cancel the context to trigger
// graceful shutdown. The command must:
//
//   - print the bound URL on stdout BEFORE blocking (so users see it
//     immediately rather than after the server stops),
//   - serve /healthz at the printed address,
//   - shut down cleanly on context cancel (no hang, no error).
//
// Catches the regression class where someone reorders Listen/Serve
// or the URL print, leaving users staring at a blank terminal
// wondering whether the UI is up.
func TestUI_BindsAndShutdownsCleanly(t *testing.T) {
	cmd := buildUI()
	var out, errBuf safeBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--port", "0"})

	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()

	// Wait for the URL to appear on stdout. Polling with a small
	// timeout is the right shape — no fixed sleep that's either
	// too short (race) or too long (slow test).
	deadline := time.Now().Add(3 * time.Second)
	var url string
	for time.Now().Before(deadline) {
		if u := extractURL(out.String()); u != "" {
			url = u
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if url == "" {
		cancel()
		<-done
		t.Fatalf("URL never appeared on stdout within deadline; stdout=%q stderr=%q",
			out.String(), errBuf.String())
	}

	resp, err := http.Get(url + "healthz")
	if err != nil {
		cancel()
		<-done
		t.Fatalf("GET /healthz at %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("healthz: status=%d body=%q", resp.StatusCode, body)
	}

	// Trigger graceful shutdown via context cancel — same path
	// SIGINT takes in production.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("command returned error on cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command did not exit within 3s of cancel — shutdown hung")
	}
	if !strings.Contains(out.String(), "stopped") {
		t.Errorf("stdout missing 'stopped' marker after shutdown, got %q", out.String())
	}
}

// TestUI_LoopbackHostInUsageDoc is a documentation pin: the long
// help text must mention loopback and the SSH-tunnel guidance. The
// CLI binding NOT being widened from loopback is the security
// posture; the help text is where users learn it.
func TestUI_LoopbackHostInUsageDoc(t *testing.T) {
	cmd := buildUI()
	if !strings.Contains(cmd.Long, "loopback") {
		t.Error("Long help missing 'loopback' — users won't know the bind policy")
	}
	if !strings.Contains(cmd.Long, "SSH tunnel") && !strings.Contains(cmd.Long, "ssh -L") &&
		!strings.Contains(cmd.Long, "SSH-tunnel") {
		t.Error("Long help missing SSH-tunnel guidance for remote-access users")
	}
}

// extractURL pulls the http://host:port/ from a stdout line that
// looks like "Web UI listening  http://127.0.0.1:54321/". We do a
// substring scan rather than a regex because the surrounding chrome
// is colored (ANSI) and the scan is cheaper to reason about.
func extractURL(stdout string) string {
	const marker = "http://"
	i := strings.Index(stdout, marker)
	if i < 0 {
		return ""
	}
	rest := stdout[i:]
	// URL ends at the next whitespace or ANSI escape.
	end := len(rest)
	for j, r := range rest {
		if r == ' ' || r == '\n' || r == '\t' || r == 0x1b {
			end = j
			break
		}
	}
	return rest[:end]
}

// TestUI_LoopbackFlagRejectsNonLoopback is the CLI-level pin for
// PR #41 #a: `dpm localnet ui --host 0.0.0.0` (without
// --allow-non-loopback) must fail at Listen() with the
// ErrNonLoopbackBind error surfaced to stderr. Catches the
// regression class where someone widens the gate by accident.
func TestUI_LoopbackFlagRejectsNonLoopback(t *testing.T) {
	cmd := buildUI()
	var out, errBuf safeBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	// 0.0.0.0 = wildcard bind = explicitly non-loopback.
	cmd.SetArgs([]string{"--port", "0", "--host", "0.0.0.0"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err == nil {
		t.Fatal("ui --host 0.0.0.0 succeeded — loopback gate regressed")
	}
	if !strings.Contains(errBuf.String(), "non-loopback") {
		t.Errorf("stderr should explain the gate, got %q", errBuf.String())
	}
}

// TestUI_NonLoopbackHostReturnsUserExitCode is the reviewer pin
// (PR #41 round-2 #4): bind failures must wrap with the right
// ExitCodeError so the outer cobra exit-code plumbing surfaces
// 1 (user error) vs 4 (runtime failure). Without this, the CLI
// loses the distinction users branch on (`dpm ui || ...`).
func TestUI_NonLoopbackHostReturnsUserExitCode(t *testing.T) {
	cmd := buildUI()
	var out, errBuf safeBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--port", "0", "--host", "0.0.0.0"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-loopback bind")
	}
	var ece localnet.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if int(ece) != localnet.ExitUserError {
		t.Errorf("exit code = %d, want ExitUserError (%d) — bind-refused is user input",
			int(ece), localnet.ExitUserError)
	}
}
