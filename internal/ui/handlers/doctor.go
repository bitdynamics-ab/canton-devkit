package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// MountDoctor installs GET /api/doctor — the Web UI counterpart of
// `dpm localnet doctor`.
//
// GET /api/preflight already exposes the resource/Docker gate, but
// `localnet doctor` layers two extra advisory checks on top of it
// (platform-support matrix + host-port availability) via the neutral
// localnet.CollectDoctor collector. This endpoint calls the SAME
// collector the CLI uses, so the two surfaces can't drift.
//
// Like the CLI verb, the report is purely diagnostic: it always
// returns 200 with a types.PreflightReport body whose `ok` field is
// the pass/fail signal — a failing host is data, not an HTTP error.
func MountDoctor(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/doctor", handleDoctor)
}

// doctorTimeout caps how long the handler waits for the docker
// subprocess probes plus the loopback-port probe to finish. Matches
// the preflight handler's budget (per-check timeout is already 10s).
const doctorTimeout = 30 * time.Second

// collectDoctor is a package-level test seam. Production resolves to
// localnet.CollectDoctor; handler tests override it so they don't need
// a real Docker daemon (mirrors the runPreflightForVersion seam).
var collectDoctor = localnet.CollectDoctor

func handleDoctor(w http.ResponseWriter, r *http.Request) {
	// Validate the version up front so a typo'd ?version= surfaces the
	// same 400 the preflight endpoint gives, instead of silently
	// falling back to "latest" inside the collector.
	version := r.URL.Query().Get("version")
	if version == "" {
		version = "latest"
	}
	if _, err := splice.Resolve(version); err != nil {
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

	// Optional ?port_base=<N> mirrors the CLI's --port-base flag: when
	// set, the collector probes the FIXED host-port block `up
	// --port-base N` would claim instead of ephemeral availability. A
	// non-numeric value is a client error, not a silent zero.
	portBase := 0
	if raw := r.URL.Query().Get("port_base"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"port_base must be an integer: "+raw,
				"pass a numeric ?port_base= (the host port block that up --port-base N would claim)")
			return
		}
		portBase = n
	}

	ctx, cancel := context.WithTimeout(r.Context(), doctorTimeout)
	defer cancel()

	report, err := collectDoctor(ctx, localnet.DoctorOptions{
		Version:  version,
		PortBase: portBase,
	})
	if err != nil {
		// Resolution already passed above, so a non-nil error here is a
		// server-side collector failure (e.g. catalogue read) — 500,
		// cause logged not shipped.
		writeError(w, http.StatusInternalServerError, "run doctor checks", err)
		return
	}
	writeJSON(w, http.StatusOK, asDoctorReport(report))
}

// asDoctorReport stamps a doctor-specific summary onto a passing report
// so the Web UI Doctor screen reads as a host-diagnostic rather than a
// pre-create gate. The failing/warning summary already produced by
// PreflightReportFromDocker (issue/warning counts) is left intact — it
// is the actionable line operators act on.
func asDoctorReport(report types.PreflightReport) types.PreflightReport {
	if report.OK && !hasWarnings(report) {
		report.Summary = "All checks passed — host is ready for `localnet up`"
	}
	return report
}

// hasWarnings reports whether any check in the report is a warn. Used to
// keep the all-pass summary distinct from the "ready, advisories above"
// case PreflightReportFromDocker already phrases.
func hasWarnings(report types.PreflightReport) bool {
	for _, sec := range report.Sections {
		for _, c := range sec.Checks {
			if c.Result == "warn" {
				return true
			}
		}
	}
	return false
}
