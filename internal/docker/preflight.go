package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

const preflightCheckTimeout = 10 * time.Second

// DefaultMinDiskBytes / DefaultMinMemoryBytes are the shared
// resource thresholds used by BOTH `localnet up` preflight and
// `localnet doctor`. They MUST be equal across the two surfaces —
// the `doctor && up` shell-gating contract (PR #39 review:
// "doctor must not fail on a host where `up` would pass") relies
// on it. A regression where one site drifted was caught in the
// PR #39 follow-up review; TestThresholdParity_DoctorMatchesUp
// pins the equality.
//
// Values chosen to match the current `up` defaults (the
// historical authority — `up` shipped these before `doctor`
// existed). If the proposal requires a bump, edit ONCE here and
// the gate stays consistent.
const (
	DefaultMinDiskBytes   uint64 = 10 * 1024 * 1024 * 1024 // 10 GiB
	DefaultMinMemoryBytes uint64 = 4 * 1024 * 1024 * 1024  // 4 GiB
)

// Status describes the outcome of a single preflight check.
type Status int

const (
	StatusOK Status = iota
	StatusWarn
	StatusFail
	StatusSkipped
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusWarn:
		return "WARN"
	case StatusFail:
		return "FAIL"
	case StatusSkipped:
		return "SKIP"
	}
	return "?"
}

// CheckResult is the outcome of one named check.
type CheckResult struct {
	Name        string
	Status      Status
	Detail      string
	Remediation string
}

// Report aggregates all preflight check results.
type Report struct {
	Results        []CheckResult
	DockerVersion  string
	ComposeVersion string
}

// OK reports whether every check passed or was skipped.
func (r *Report) OK() bool {
	for _, c := range r.Results {
		if c.Status == StatusFail {
			return false
		}
	}
	return true
}

// HasWarnings reports whether any check returned a warning.
func (r *Report) HasWarnings() bool {
	for _, c := range r.Results {
		if c.Status == StatusWarn {
			return true
		}
	}
	return false
}

// Write renders the report to w. Failures and warnings always include
// remediation hints; passing checks render as a single status line.
func (r *Report) Write(w io.Writer) {
	for _, c := range r.Results {
		_, _ = fmt.Fprintf(w, "  [%s] %s", c.Status, c.Name)
		if c.Detail != "" {
			_, _ = fmt.Fprintf(w, ": %s", c.Detail)
		}
		_, _ = fmt.Fprintln(w)
		if (c.Status == StatusFail || c.Status == StatusWarn) && c.Remediation != "" {
			for _, line := range strings.Split(strings.TrimSpace(c.Remediation), "\n") {
				_, _ = fmt.Fprintf(w, "        → %s\n", line)
			}
		}
	}
}

// Options controls which checks run and the thresholds applied.
//
// We deliberately do NOT preflight TCP ports: DevKit allocates host ports
// for the user (ephemerally via net.Listen(":0")) so port availability is
// never a precondition of `localnet up`. See BIT-30 discussion.
type Options struct {
	// DataDir is the path whose filesystem is checked for free space.
	DataDir string
	// MinDiskBytes is the minimum free disk required. 0 disables the check.
	MinDiskBytes uint64
	// MinMemoryBytes is the minimum Docker daemon memory required. 0 disables.
	MinMemoryBytes uint64
}

// RunPreflight runs all preflight checks and returns a Report. It never
// modifies the host; failing checks emit remediation hints only.
func RunPreflight(ctx context.Context, opts Options) *Report {
	report := &Report{}

	// Docker CLI must exist before any other docker-* check can run.
	cliResult := checkDockerCLI()
	report.Results = append(report.Results, cliResult)
	if cliResult.Status == StatusFail {
		report.Results = append(report.Results,
			skip("Docker daemon", "docker CLI missing"),
			skip("Docker Compose v2", "docker CLI missing"),
			skip("Docker memory", "docker CLI missing"),
		)
		report.Results = append(report.Results, runHostChecks(opts)...)
		return report
	}

	daemonResult, dockerVersion := checkDockerDaemon(ctx)
	report.Results = append(report.Results, daemonResult)
	report.DockerVersion = dockerVersion

	composeResult, composeVersion := checkComposeV2(ctx)
	report.Results = append(report.Results, composeResult)
	report.ComposeVersion = composeVersion

	if daemonResult.Status == StatusOK {
		report.Results = append(report.Results, checkDockerMemory(ctx, opts.MinMemoryBytes))
	} else {
		report.Results = append(report.Results, skip("Docker memory", "daemon unavailable"))
	}

	report.Results = append(report.Results, runHostChecks(opts)...)
	return report
}

func runHostChecks(opts Options) []CheckResult {
	var out []CheckResult
	if opts.DataDir != "" && opts.MinDiskBytes > 0 {
		out = append(out, checkDiskSpace(opts.DataDir, opts.MinDiskBytes))
	}
	out = append(out, checkHostPrereqs())
	return out
}

func skip(name, reason string) CheckResult {
	return CheckResult{Name: name, Status: StatusSkipped, Detail: reason}
}
