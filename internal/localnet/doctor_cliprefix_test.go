package localnet

import (
	"errors"
	"net"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
)

func TestPreflightReadySummary_FollowsCLIPrefix(t *testing.T) {
	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		rep := PreflightReportFromDocker(&docker.Report{Results: []docker.CheckResult{
			{Name: "Docker daemon", Status: docker.StatusOK},
		}})
		want := "All checks passed — host is ready for `" + prefix + " up <name>`"
		if rep.Summary != want {
			t.Errorf("summary = %q, want %q", rep.Summary, want)
		}
		if got := PreflightReadySummary(); got != want {
			t.Errorf("PreflightReadySummary() = %q, want %q", got, want)
		}
	})
}

func TestDoctorRemediations_FollowCLIPrefix(t *testing.T) {
	refuse := func(string, string) (net.Listener, error) { return nil, errors.New("sandboxed") }
	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		assertCLIHints(t, platformSupportCheck("plan9", "mips").Remediation,
			[]string{"Report issues with `" + prefix + " doctor` output."}, other)
		assertCLIHints(t, ephemeralPortAvailabilityCheck(refuse).Remediation,
			[]string{"before running `" + prefix + " up <name>`."}, other)
		assertCLIHints(t, fixedPortAvailabilityCheck(refuse, 20000).Remediation,
			[]string{"`" + prefix + " up <name> --port-base 20000` would fail until they're free."}, other)
	})
}
