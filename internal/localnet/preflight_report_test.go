package localnet

import (
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
)

func TestPreflightReportFromDocker_CategorisesUpstreamNames(t *testing.T) {
	rep := PreflightReportFromDocker(&docker.Report{Results: []docker.CheckResult{
		{Name: "Docker CLI", Status: docker.StatusOK},
		{Name: "Docker daemon", Status: docker.StatusOK},
		{Name: "Docker Compose v2", Status: docker.StatusOK},
		{Name: "Docker memory", Status: docker.StatusOK},
		{Name: "Disk space", Status: docker.StatusOK},
		{Name: "Host prerequisites (linux)", Status: docker.StatusOK},
		{Name: "Port 65535 free", Status: docker.StatusOK},
	}})

	bucket := map[string]string{}
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			bucket[c.Label] = sec.Title
		}
	}
	want := map[string]string{
		"Docker CLI":                 "System",
		"Docker daemon":              "System",
		"Docker Compose v2":          "System",
		"Docker memory":              "Resources",
		"Disk space":                 "Resources",
		"Host prerequisites (linux)": "System",
		"Port 65535 free":            "Network",
	}
	for label, wantSection := range want {
		if bucket[label] != wantSection {
			t.Errorf("%q section = %q, want %q", label, bucket[label], wantSection)
		}
	}
}

func TestPreflightReportFromDocker_ResultTokensAndRemediation(t *testing.T) {
	rep := PreflightReportFromDocker(&docker.Report{Results: []docker.CheckResult{
		{Name: "Docker CLI", Status: docker.StatusOK},
		{Name: "Docker memory", Status: docker.StatusWarn, Remediation: " increase memory \n\n retry "},
		{Name: "Port 65535 free", Status: docker.StatusFail, Remediation: "free port"},
		{Name: "Some future check", Status: docker.StatusSkipped, Remediation: "ignored for skipped"},
	}})

	got := map[string]struct {
		result      string
		remediation []string
	}{}
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			got[c.Label] = struct {
				result      string
				remediation []string
			}{result: c.Result, remediation: c.Remediation}
		}
	}
	checks := map[string]string{
		"Docker CLI":        "pass",
		"Docker memory":     "warn",
		"Port 65535 free":   "fail",
		"Some future check": "skip",
	}
	for label, want := range checks {
		if got[label].result != want {
			t.Errorf("%q result = %q, want %q", label, got[label].result, want)
		}
	}
	if strings.Join(got["Docker memory"].remediation, ",") != "increase memory,retry" {
		t.Errorf("warn remediation = %#v, want trimmed non-empty lines", got["Docker memory"].remediation)
	}
	if len(got["Some future check"].remediation) != 0 {
		t.Errorf("skipped remediation = %#v, want omitted", got["Some future check"].remediation)
	}
}
