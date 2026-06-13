package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
)

// doctorMux mounts only the doctor handler — the other Mount* calls
// would require docker / hub wiring irrelevant to these tests. Same
// isolation pattern the snapshot/metrics handler tests use.
func doctorMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountDoctor(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// stubCollectDoctor swaps the package-level collector seam for the
// duration of a test and restores it after. The real CollectDoctor
// shells out to docker, which CI unit runs don't have.
func stubCollectDoctor(t *testing.T, fn func(context.Context, localnet.DoctorOptions) (types.PreflightReport, error)) {
	t.Helper()
	orig := collectDoctor
	collectDoctor = fn
	t.Cleanup(func() { collectDoctor = orig })
}

func TestHandleDoctor_OKReturnsReportWithDoctorOnlyChecks(t *testing.T) {
	var gotOpts localnet.DoctorOptions
	stubCollectDoctor(t, func(_ context.Context, opts localnet.DoctorOptions) (types.PreflightReport, error) {
		gotOpts = opts
		// Shape mirrors what localnet.CollectDoctor produces: the
		// shared preflight checks PLUS the doctor-only advisories
		// (Platform support, Ephemeral loopback ports).
		return types.PreflightReport{
			SchemaVersion: types.SchemaVersion,
			OK:            true,
			Sections: []types.PreflightSection{
				{Title: "System", Checks: []types.PreflightCheck{
					{Label: "Docker daemon", Result: "pass"},
					{Label: "Platform support", Result: "pass", Detail: "darwin/arm64 is a supported platform"},
				}},
				{Title: "Network", Checks: []types.PreflightCheck{
					{Label: "Ephemeral loopback ports", Result: "pass"},
				}},
			},
		}, nil
	})

	srv := doctorMux(t)
	resp, err := http.Get(srv.URL + "/api/doctor")
	if err != nil {
		t.Fatalf("GET /api/doctor: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var rep types.PreflightReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.SchemaVersion != types.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", rep.SchemaVersion, types.SchemaVersion)
	}
	if !rep.OK {
		t.Error("ok = false, want true")
	}
	// The doctor-only advisories must be present — the whole point of
	// the endpoint over /api/preflight.
	labels := map[string]bool{}
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			labels[c.Label] = true
		}
	}
	if !labels["Platform support"] {
		t.Error("report missing doctor-only 'Platform support' check")
	}
	if !labels["Ephemeral loopback ports"] {
		t.Error("report missing doctor-only 'Ephemeral loopback ports' check")
	}
	// Default version defaults to latest; no fixed-port mode.
	if gotOpts.Version != "latest" {
		t.Errorf("collector Version = %q, want latest", gotOpts.Version)
	}
	if gotOpts.PortBase != 0 {
		t.Errorf("collector PortBase = %d, want 0 (ephemeral mode)", gotOpts.PortBase)
	}
	// All-pass report gets the doctor summary.
	if rep.Summary == "" {
		t.Error("summary empty for all-pass report")
	}
}

func TestHandleDoctor_FailingHostStillReturns200(t *testing.T) {
	stubCollectDoctor(t, func(_ context.Context, _ localnet.DoctorOptions) (types.PreflightReport, error) {
		return types.PreflightReport{
			SchemaVersion: types.SchemaVersion,
			OK:            false,
			ErrorCode:     localnet.ErrCodeMemoryLow,
			Summary:       "1 issue · 0 warnings — host is NOT ready",
			Sections: []types.PreflightSection{
				{Title: "Resources", Checks: []types.PreflightCheck{
					{Label: "Docker memory", Result: "fail", Detail: "too low",
						Remediation: []string{"raise Docker Desktop memory"}},
				}},
			},
		}, nil
	})

	srv := doctorMux(t)
	resp, err := http.Get(srv.URL + "/api/doctor")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// A failing host is DATA, not an HTTP error — the screen renders
	// the failing checks. 200 with ok=false, like the preflight gate.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (failing host is data)", resp.StatusCode)
	}
	var rep types.PreflightReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.OK {
		t.Error("ok = true, want false")
	}
	if rep.ErrorCode != localnet.ErrCodeMemoryLow {
		t.Errorf("error_code = %q, want %q", rep.ErrorCode, localnet.ErrCodeMemoryLow)
	}
	// The handler must NOT overwrite the failing summary with the
	// all-pass line.
	if rep.Summary == "All checks passed — host is ready for `localnet up`" {
		t.Error("failing summary overwritten with all-pass line")
	}
}

func TestHandleDoctor_PortBaseQueryThreadsToCollector(t *testing.T) {
	var gotPortBase int
	stubCollectDoctor(t, func(_ context.Context, opts localnet.DoctorOptions) (types.PreflightReport, error) {
		gotPortBase = opts.PortBase
		return types.PreflightReport{SchemaVersion: types.SchemaVersion, OK: true}, nil
	})

	srv := doctorMux(t)
	resp, err := http.Get(srv.URL + "/api/doctor?port_base=3975")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotPortBase != 3975 {
		t.Errorf("collector PortBase = %d, want 3975", gotPortBase)
	}
}

func TestHandleDoctor_BadPortBaseIs400(t *testing.T) {
	called := false
	stubCollectDoctor(t, func(_ context.Context, _ localnet.DoctorOptions) (types.PreflightReport, error) {
		called = true
		return types.PreflightReport{}, nil
	})

	srv := doctorMux(t)
	resp, err := http.Get(srv.URL + "/api/doctor?port_base=notanint")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if called {
		t.Error("collector ran despite invalid port_base")
	}
	var body errorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != ErrCodeInvalidRequest {
		t.Errorf("code = %q, want %q", body.Code, ErrCodeInvalidRequest)
	}
	// The bad-port_base error must carry a remediation hint, for parity
	// with the unknown-version branch (both are user-fixable input errors).
	if len(body.Remediation) == 0 {
		t.Error("port_base 400 carries no remediation hint; want one")
	}
}

func TestHandleDoctor_UnknownVersionIs400(t *testing.T) {
	called := false
	stubCollectDoctor(t, func(_ context.Context, _ localnet.DoctorOptions) (types.PreflightReport, error) {
		called = true
		return types.PreflightReport{}, nil
	})

	srv := doctorMux(t)
	// A bogus tag that splice.Resolve treats as uncurated should be
	// rejected with 400 before the collector runs — mirrors the
	// preflight endpoint's guard so a typo can't silently default.
	resp, err := http.Get(srv.URL + "/api/doctor?version=0.0.0-does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown version", resp.StatusCode)
	}
	if called {
		t.Error("collector ran despite unknown version")
	}
}

func TestHandleDoctor_CollectorErrorIs500(t *testing.T) {
	stubCollectDoctor(t, func(_ context.Context, _ localnet.DoctorOptions) (types.PreflightReport, error) {
		return types.PreflightReport{}, errors.New("catalogue read failed")
	})

	srv := doctorMux(t)
	resp, err := http.Get(srv.URL + "/api/doctor")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	// The cause string must not leak to the client (5xx policy).
	var body errorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Detail != "" {
		t.Errorf("detail = %q, want empty (no cause leak on 5xx)", body.Detail)
	}
}
