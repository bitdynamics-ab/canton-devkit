package localnet

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
	var out, errBuf bytes.Buffer
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
