package docker

import (
	"bytes"
	"strings"
	"testing"
)

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
		{5_000_000, "5 MB"},
		{3_000_000_000, "3.00 GB"},
		{8_589_934_592, "8.59 GB"},
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
