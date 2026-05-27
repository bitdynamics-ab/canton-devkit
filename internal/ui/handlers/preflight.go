package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// MountPreflight installs GET /api/preflight.
//
// The endpoint runs the same `docker.RunPreflight` gate that
// `localnet up` and `localnet doctor` use, but with the
// per-Splice-version memory threshold from the curated catalogue.
// Web UI calls it before posting POST /api/instances so a user
// on a host that can't satisfy Splice 0.6.4's ~8 GiB Docker
// memory floor sees a blocker dialog BEFORE the multi-minute
// bring-up — instead of waiting for `wait_healthy` to time out
// because the canton container OOM-loops.
//
// handleCreate runs the same check server-side so a client that
// skips the preflight call (or races a memory-pressure event)
// still gets a 422 with the same shape, not a useless 202 that
// then crashes mid-bring-up.
func MountPreflight(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/preflight", handlePreflight)
}

// preflightTimeout caps how long the handler will wait for the
// docker subprocess probes to finish. The internal per-check
// timeout is already 10s; we give a small overhead on top.
const preflightTimeout = 30 * time.Second

func handlePreflight(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("version")
	v, err := splice.Resolve(version)
	if err != nil {
		if errors.Is(err, splice.ErrUncuratedTag) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"unknown splice version: "+version,
				"call GET /api/splice/versions for the curated list")
			return
		}
		writeError(w, http.StatusBadRequest, "resolve splice version", err)
		return
	}

	report := runPreflightForVersion(r.Context(), v)
	writeJSON(w, http.StatusOK, report)
}

// runPreflightForVersion is a package-level test seam. Production
// resolves to runPreflightForVersionImpl below; tests in main_test.go
// override it with a no-op so handler tests (TestCancelUp,
// TestCreate_*) don't fail on CI runners that legitimately don't
// meet the docker memory floor.
var runPreflightForVersion = runPreflightForVersionImpl

// runPreflightForVersionImpl produces a types.PreflightReport tailored
// to the given Splice version. It's called via the runPreflightForVersion
// seam from handleCreate (before queueing the bring-up goroutine) and
// from handlePreflight (the explicit GET endpoint). Same gate applies
// regardless of which client path triggered the create.
func runPreflightForVersionImpl(ctx context.Context, v splice.Version) types.PreflightReport {
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	min := v.MinMemoryBytes
	if min == 0 {
		min = docker.DefaultMinMemoryBytes
	}

	rpt := docker.RunPreflight(ctx, docker.Options{
		DataDir:                registry.Root(),
		MinDiskBytes:           docker.DefaultMinDiskBytes,
		MinMemoryBytes:         min,
		RecommendedMemoryBytes: v.RecommendedMemoryBytes,
	})

	return toAPIReport(rpt, v)
}

// toAPIReport adapts the internal docker.Report shape to the
// stable JSON shape consumed by the CLI `--json` flag and the
// Web UI. Splits checks into the two sections the mockup renders
// ("System" — daemon/CLI/compose; "Resources" — memory/disk/host).
func toAPIReport(r *docker.Report, v splice.Version) types.PreflightReport {
	system := types.PreflightSection{Title: "System"}
	resources := types.PreflightSection{Title: "Resources"}

	for _, c := range r.Results {
		check := types.PreflightCheck{
			Label:  c.Name,
			Result: statusToResult(c.Status),
			Detail: c.Detail,
		}
		if c.Remediation != "" {
			check.Remediation = splitNonEmptyLines(c.Remediation)
		}
		switch c.Name {
		case "Docker memory", "Disk space":
			resources.Checks = append(resources.Checks, check)
		case "Host prerequisites (linux)", "Host prerequisites (darwin)", "Host prerequisites (windows)":
			resources.Checks = append(resources.Checks, check)
		default:
			system.Checks = append(system.Checks, check)
		}
	}

	report := types.PreflightReport{
		SchemaVersion: types.SchemaVersion,
		OK:            r.OK(),
		Sections:      []types.PreflightSection{system, resources},
	}
	if !report.OK {
		report.Summary = "host does not meet Splice " + v.Tag + " requirements"
		// BIT-172: stamp the most-specific structured code from
		// the same priority table RunUp uses, so the Web UI's
		// PreflightPanel can render targeted remediation (Docker
		// not running vs memory too low) instead of generic
		// failure copy.
		report.ErrorCode = preflightCodeFromReport(r)
	} else if r.HasWarnings() {
		report.Summary = "host meets minimums for Splice " + v.Tag + " but raise resources for headroom"
	} else {
		report.Summary = "host ready for Splice " + v.Tag
	}
	return report
}

// preflightCodeFromReport is now a thin alias for the canonical
// implementation in internal/localnet. Originally duplicated here
// "to avoid an upward dep on internal/localnet from the handler
// package" — but the handler package already imports
// internal/localnet (e.g. for ValidateName), so the dedupe is
// free. Sharing one function means SSE + HTTP can never disagree
// on the code emitted for the same docker.Report. BIT-172 review.
var preflightCodeFromReport = localnet.PreflightCodeFromReport

func statusToResult(s docker.Status) string {
	switch s {
	case docker.StatusOK:
		return "pass"
	case docker.StatusWarn:
		return "warn"
	case docker.StatusFail:
		return "fail"
	case docker.StatusSkipped:
		return "skip"
	}
	return "skip"
}

func splitNonEmptyLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			// trim trailing spaces; cheap manual trim avoids importing strings
			for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t') {
				line = line[:len(line)-1]
			}
			for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				line = line[1:]
			}
			if line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	return out
}
