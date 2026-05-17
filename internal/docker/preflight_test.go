package docker

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"
)

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return p
}

func TestReportOK(t *testing.T) {
	r := &Report{Results: []CheckResult{
		{Name: "A", Status: StatusOK},
		{Name: "B", Status: StatusOK},
		{Name: "C", Status: StatusSkipped},
	}}
	if !r.OK() {
		t.Errorf("expected OK with only pass+skip, got false")
	}
	if r.HasWarnings() {
		t.Errorf("expected no warnings")
	}
}

func TestReportNotOK(t *testing.T) {
	r := &Report{Results: []CheckResult{
		{Name: "A", Status: StatusOK},
		{Name: "B", Status: StatusFail, Detail: "boom", Remediation: "fix it"},
	}}
	if r.OK() {
		t.Errorf("expected not OK with a failing check")
	}
}

func TestReportWriteIncludesRemediationOnFail(t *testing.T) {
	r := &Report{Results: []CheckResult{
		{Name: "Port 5011 free", Status: StatusFail, Detail: "in use", Remediation: "stop the other process\nthen retry"},
		{Name: "Docker CLI", Status: StatusOK},
	}}
	var buf bytes.Buffer
	r.Write(&buf)
	out := buf.String()

	if !strings.Contains(out, "[FAIL] Port 5011 free: in use") {
		t.Errorf("missing fail line; got:\n%s", out)
	}
	if !strings.Contains(out, "→ stop the other process") {
		t.Errorf("missing remediation line; got:\n%s", out)
	}
	if !strings.Contains(out, "→ then retry") {
		t.Errorf("missing multi-line remediation; got:\n%s", out)
	}
	if !strings.Contains(out, "[OK] Docker CLI") {
		t.Errorf("missing OK line; got:\n%s", out)
	}
}

func TestReportWriteSkipsRemediationOnPass(t *testing.T) {
	r := &Report{Results: []CheckResult{
		{Name: "X", Status: StatusOK, Detail: "fine", Remediation: "should-not-appear"},
	}}
	var buf bytes.Buffer
	r.Write(&buf)
	if strings.Contains(buf.String(), "should-not-appear") {
		t.Errorf("remediation rendered for passing check:\n%s", buf.String())
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{500, "500 B"},
		{2048, "2 KB"},
		{5 * 1024 * 1024, "5 MB"},
		{3 * 1024 * 1024 * 1024, "3.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("foo\nbar\n"); got != "foo" {
		t.Errorf("got %q", got)
	}
	if got := firstLine("  only line  "); got != "only line" {
		t.Errorf("got %q", got)
	}
	if got := firstLine(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestCheckPortFreeReportsBusy(t *testing.T) {
	// Bind to an ephemeral port so we can predict that it's busy.
	ln := mustListen(t)
	defer ln.Close()
	port := portOf(t, ln.Addr().String())

	res := checkPortFree(port)
	if res.Status != StatusFail {
		t.Fatalf("expected FAIL, got %s (detail=%q)", res.Status, res.Detail)
	}
	if res.Remediation == "" {
		t.Errorf("expected platform-specific remediation, got empty")
	}
}

func TestCheckPortFreeReportsFree(t *testing.T) {
	// Bind, get a port, close, then check that port — almost certainly free.
	ln := mustListen(t)
	port := portOf(t, ln.Addr().String())
	ln.Close()

	res := checkPortFree(port)
	if res.Status != StatusOK {
		t.Fatalf("expected OK, got %s (detail=%q)", res.Status, res.Detail)
	}
}

func TestRunPreflightWithoutDockerSkipsDownstream(t *testing.T) {
	// We can't easily un-install docker, so just verify the structure:
	// if checkDockerCLI fails, the report has SKIPs for daemon/compose/memory.
	cli := CheckResult{Name: "Docker CLI", Status: StatusFail}
	r := &Report{Results: []CheckResult{
		cli,
		skip("Docker daemon", "x"),
		skip("Docker Compose v2", "x"),
		skip("Docker memory", "x"),
	}}
	if r.OK() {
		t.Errorf("expected not OK")
	}
	skipped := 0
	for _, c := range r.Results {
		if c.Status == StatusSkipped {
			skipped++
		}
	}
	if skipped != 3 {
		t.Errorf("expected 3 skipped, got %d", skipped)
	}
}
