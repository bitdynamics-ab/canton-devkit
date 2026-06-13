package localnet

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestCollectDoctor_UsesVersionAwareUpThresholds(t *testing.T) {
	version, err := splice.Resolve(splice.LatestAlias)
	if err != nil {
		t.Fatalf("resolve latest: %v", err)
	}
	var got docker.Options
	_, err = CollectDoctor(context.Background(), DoctorOptions{
		GOOS:       "linux",
		GOARCH:     "amd64",
		ListenFunc: fakeListenOK,
		Prober: func(_ context.Context, opts docker.Options) *docker.Report {
			got = opts
			return &docker.Report{Results: []docker.CheckResult{{Name: "Docker CLI", Status: docker.StatusOK}}}
		},
	})
	if err != nil {
		t.Fatalf("CollectDoctor: %v", err)
	}
	if got.MinDiskBytes != docker.DefaultMinDiskBytes {
		t.Errorf("MinDiskBytes = %d, want %d", got.MinDiskBytes, docker.DefaultMinDiskBytes)
	}
	if got.MinMemoryBytes != splice.MinMemoryFor(version) {
		t.Errorf("MinMemoryBytes = %d, want %d", got.MinMemoryBytes, splice.MinMemoryFor(version))
	}
	if got.RecommendedMemoryBytes != splice.RecommendedMemoryFor(version) {
		t.Errorf("RecommendedMemoryBytes = %d, want %d", got.RecommendedMemoryBytes, splice.RecommendedMemoryFor(version))
	}
}

func TestCollectDoctor_AppendsDoctorAdvisories(t *testing.T) {
	rep, err := CollectDoctor(context.Background(), DoctorOptions{
		GOOS:       "linux",
		GOARCH:     "amd64",
		ListenFunc: fakeListenOK,
		Prober: func(context.Context, docker.Options) *docker.Report {
			return &docker.Report{Results: []docker.CheckResult{{Name: "Docker CLI", Status: docker.StatusOK}}}
		},
	})
	if err != nil {
		t.Fatalf("CollectDoctor: %v", err)
	}
	bucket := map[string]string{}
	result := map[string]string{}
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			bucket[c.Label] = sec.Title
			result[c.Label] = c.Result
		}
	}
	if bucket["Platform support"] != "System" || result["Platform support"] != "pass" {
		t.Errorf("Platform support = section %q result %q, want System/pass", bucket["Platform support"], result["Platform support"])
	}
	if bucket["Ephemeral loopback ports"] != "Network" || result["Ephemeral loopback ports"] != "pass" {
		t.Errorf("Ephemeral loopback ports = section %q result %q, want Network/pass", bucket["Ephemeral loopback ports"], result["Ephemeral loopback ports"])
	}
	if !rep.OK {
		t.Error("OK = false, want true for pass-only checks")
	}
}

func TestCollectDoctor_UnsupportedPlatformWarnsWithoutFailing(t *testing.T) {
	rep, err := CollectDoctor(context.Background(), DoctorOptions{
		GOOS:       "plan9",
		GOARCH:     "riscv64",
		ListenFunc: fakeListenOK,
		Prober: func(context.Context, docker.Options) *docker.Report {
			return &docker.Report{Results: []docker.CheckResult{{Name: "Docker CLI", Status: docker.StatusOK}}}
		},
	})
	if err != nil {
		t.Fatalf("CollectDoctor: %v", err)
	}
	check := findPreflightCheck(t, rep, "Platform support")
	if check.Result != "warn" {
		t.Errorf("Platform support result = %q, want warn", check.Result)
	}
	if !rep.OK {
		t.Error("OK = false, want true because platform support is advisory")
	}
	if rep.Summary != "0 issues · 1 warning — host is ready (advisories above)" {
		t.Errorf("Summary = %q", rep.Summary)
	}
}

func TestCollectDoctor_PortBindFailureWarnsWithoutFailing(t *testing.T) {
	rep, err := CollectDoctor(context.Background(), DoctorOptions{
		GOOS:   "linux",
		GOARCH: "amd64",
		ListenFunc: func(string, string) (net.Listener, error) {
			return nil, errors.New("bind: operation not permitted")
		},
		Prober: func(context.Context, docker.Options) *docker.Report {
			return &docker.Report{Results: []docker.CheckResult{{Name: "Docker CLI", Status: docker.StatusOK}}}
		},
	})
	if err != nil {
		t.Fatalf("CollectDoctor: %v", err)
	}
	check := findPreflightCheck(t, rep, "Ephemeral loopback ports")
	if check.Result != "warn" {
		t.Errorf("Ephemeral loopback ports result = %q, want warn", check.Result)
	}
	if !rep.OK {
		t.Error("OK = false, want true because port availability is advisory")
	}
}

func TestCollectDoctor_InvalidVersion(t *testing.T) {
	_, err := CollectDoctor(context.Background(), DoctorOptions{Version: "nope"})
	if err == nil {
		t.Fatal("expected invalid version error")
	}
}

func findPreflightCheck(t *testing.T, rep types.PreflightReport, label string) types.PreflightCheck {
	t.Helper()
	for _, sec := range rep.Sections {
		for _, check := range sec.Checks {
			if check.Label == label {
				return check
			}
		}
	}
	t.Fatalf("check %q not found in %+v", label, rep.Sections)
	return types.PreflightCheck{}
}

type fakeListener struct{}

func fakeListenOK(string, string) (net.Listener, error) { return fakeListener{}, nil }

func (fakeListener) Accept() (net.Conn, error) { return nil, errors.New("unused") }
func (fakeListener) Close() error              { return nil }
func (fakeListener) Addr() net.Addr            { return fakeAddr("127.0.0.1:0") }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

// TestCollectDoctor_FixedPortBaseReplacesEphemeral verifies that with
// PortBase set, doctor checks the FIXED port block (and not the ephemeral
// probe) — and passes when the block is free.
func TestCollectDoctor_FixedPortBaseReplacesEphemeral(t *testing.T) {
	rep, err := CollectDoctor(context.Background(), DoctorOptions{
		GOOS:       "linux",
		GOARCH:     "amd64",
		PortBase:   20000,
		ListenFunc: fakeListenOK,
		Prober: func(context.Context, docker.Options) *docker.Report {
			return &docker.Report{Results: []docker.CheckResult{{Name: "Docker CLI", Status: docker.StatusOK}}}
		},
	})
	if err != nil {
		t.Fatalf("CollectDoctor: %v", err)
	}
	fixed := findPreflightCheck(t, rep, "Fixed ports (--port-base)")
	if fixed.Result != "pass" {
		t.Errorf("fixed-port check = %q, want pass", fixed.Result)
	}
	// The ephemeral check must NOT be present in fixed mode.
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			if c.Label == "Ephemeral loopback ports" {
				t.Error("ephemeral check should be replaced by fixed-port check when --port-base is set")
			}
		}
	}
}

// TestCollectDoctor_FixedPortBaseBusyFails verifies a busy fixed block is
// a hard FAIL (doctor exits 2) — matching what `up --port-base` would do.
func TestCollectDoctor_FixedPortBaseBusyFails(t *testing.T) {
	rep, err := CollectDoctor(context.Background(), DoctorOptions{
		GOOS:     "linux",
		GOARCH:   "amd64",
		PortBase: 20000,
		ListenFunc: func(string, string) (net.Listener, error) {
			return nil, errors.New("bind: address already in use")
		},
		Prober: func(context.Context, docker.Options) *docker.Report {
			return &docker.Report{Results: []docker.CheckResult{{Name: "Docker CLI", Status: docker.StatusOK}}}
		},
	})
	if err != nil {
		t.Fatalf("CollectDoctor: %v", err)
	}
	fixed := findPreflightCheck(t, rep, "Fixed ports (--port-base)")
	if fixed.Result != "fail" {
		t.Errorf("busy fixed-port check = %q, want fail", fixed.Result)
	}
	if rep.OK {
		t.Error("rep.OK = true, want false when fixed ports are busy")
	}
}
