package handlers

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestToAPIReport_UsesSharedAdapterAndStampsVersionFields(t *testing.T) {
	v := splice.Version{Tag: "0.test"}
	report := toAPIReport(&docker.Report{Results: []docker.CheckResult{
		{Name: "Docker memory", Status: docker.StatusFail, Detail: "too low", Remediation: "raise memory"},
		{Name: "Host prerequisites (linux)", Status: docker.StatusOK},
	}}, v)

	if report.OK {
		t.Fatal("OK = true, want false for failing report")
	}
	if report.Summary != "host does not meet Splice 0.test requirements" {
		t.Errorf("Summary = %q", report.Summary)
	}
	if report.ErrorCode != localnet.ErrCodeMemoryLow {
		t.Errorf("ErrorCode = %q, want %q", report.ErrorCode, localnet.ErrCodeMemoryLow)
	}
	bucket := map[string]string{}
	for _, sec := range report.Sections {
		for _, c := range sec.Checks {
			bucket[c.Label] = sec.Title
		}
	}
	if bucket["Docker memory"] != "Resources" {
		t.Errorf("Docker memory section = %q, want Resources", bucket["Docker memory"])
	}
	if bucket["Host prerequisites (linux)"] != "System" {
		t.Errorf("Host prerequisites section = %q, want System", bucket["Host prerequisites (linux)"])
	}
}
