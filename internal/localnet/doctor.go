package localnet

import (
	"context"
	"fmt"
	"net"
	"runtime"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// DoctorOptions configures the neutral doctor collector used by the CLI and
// future non-terminal surfaces.
type DoctorOptions struct {
	Version string
	Prober  func(context.Context, docker.Options) *docker.Report

	ListenFunc func(network, address string) (net.Listener, error)
	GOOS       string
	GOARCH     string

	// PortBase, when > 0, switches the port check from "can the host
	// allocate ephemeral loopback ports" to "is the FIXED block a
	// `localnet up --port-base <PortBase>` would claim currently free".
	// This is the fixed-required-port preflight that matches explicit-
	// port mode. 0 (default) keeps the ephemeral-availability check.
	PortBase int
}

// CollectDoctor runs the same preflight gate as `localnet up` for the selected
// Splice version, then appends doctor-only advisory checks.
func CollectDoctor(ctx context.Context, opts DoctorOptions) (types.PreflightReport, error) {
	requestedVersion := opts.Version
	if requestedVersion == "" {
		requestedVersion = "latest"
	}
	version, err := splice.Resolve(requestedVersion)
	if err != nil {
		return types.PreflightReport{}, fmt.Errorf("resolve splice version: %w", err)
	}

	prober := opts.Prober
	if prober == nil {
		prober = docker.RunPreflight
	}
	report := prober(ctx, docker.Options{
		DataDir:                registry.Root(),
		MinDiskBytes:           docker.DefaultMinDiskBytes,
		MinMemoryBytes:         splice.MinMemoryFor(version),
		RecommendedMemoryBytes: splice.RecommendedMemoryFor(version),
	})
	if report == nil {
		report = &docker.Report{}
	}

	goos, goarch := opts.GOOS, opts.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	listen := opts.ListenFunc
	if listen == nil {
		listen = net.Listen
	}
	portCheck := ephemeralPortAvailabilityCheck(listen)
	if opts.PortBase > 0 {
		// Explicit-port mode: check the exact fixed block instead.
		portCheck = fixedPortAvailabilityCheck(listen, opts.PortBase)
	}
	report.Results = append(report.Results,
		platformSupportCheck(goos, goarch),
		portCheck,
		uiReachabilityCheck(ctx),
	)

	return PreflightReportFromDocker(report), nil
}

// fixedPortAvailabilityCheck probes the deterministic block of host ports
// that `localnet up --port-base <base>` would claim — base..base+N-1,
// where N is the number of UI port env vars. A busy port is a hard FAIL
// (not a warning): unlike auto-allocation, explicit-port mode can't fall
// back, so `up --port-base <base>` would itself fail. This is the
// fixed-required-port preflight for explicit-port mode.
func fixedPortAvailabilityCheck(listen func(string, string) (net.Listener, error), base int) docker.CheckResult {
	if base < MinPortBase || base+len(UIPortEnvVars()) > 65535 {
		return docker.CheckResult{
			Name:        "Fixed ports (--port-base)",
			Status:      docker.StatusFail,
			Detail:      fmt.Sprintf("port base %d is out of range", base),
			Remediation: fmt.Sprintf("Choose a base in %d..%d.", MinPortBase, 65535-len(UIPortEnvVars())),
		}
	}
	n := len(UIPortEnvVars())
	held := make([]net.Listener, 0, n)
	defer func() {
		for _, ln := range held {
			_ = ln.Close()
		}
	}()
	var busy []int
	for i := 0; i < n; i++ {
		p := base + i
		ln, err := listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			busy = append(busy, p)
			continue
		}
		held = append(held, ln)
	}
	if len(busy) > 0 {
		return docker.CheckResult{
			Name:        "Fixed ports (--port-base)",
			Status:      docker.StatusFail,
			Detail:      fmt.Sprintf("ports already in use for base %d: %v", base, busy),
			Remediation: fmt.Sprintf("Free these ports (lsof -i :<port>) or pick a different --port-base; `up --port-base %d` would fail until they're free.", base),
		}
	}
	return docker.CheckResult{
		Name:   "Fixed ports (--port-base)",
		Status: docker.StatusOK,
		Detail: fmt.Sprintf("fixed ports %d-%d are free", base, base+n-1),
	}
}

// supportedPlatforms is DevKit's released OS/arch matrix (GOOS/GOARCH), kept
// in sync with the GoReleaser build matrix and compatibility matrix.
var supportedPlatforms = map[string]bool{
	"darwin/arm64":  true,
	"linux/amd64":   true,
	"windows/amd64": true,
}

func platformSupportCheck(goos, goarch string) docker.CheckResult {
	key := goos + "/" + goarch
	if supportedPlatforms[key] {
		return docker.CheckResult{
			Name:   "Platform support",
			Status: docker.StatusOK,
			Detail: key + " is a supported platform",
		}
	}
	return docker.CheckResult{
		Name:        "Platform support",
		Status:      docker.StatusWarn,
		Detail:      key + " is not in DevKit's tested release matrix",
		Remediation: "DevKit only orchestrates Docker, so this platform may still work. Report issues with `localnet doctor` output.",
	}
}

const portProbeCount = 4

func ephemeralPortAvailabilityCheck(listen func(string, string) (net.Listener, error)) docker.CheckResult {
	held := make([]net.Listener, 0, portProbeCount)
	defer func() {
		for _, ln := range held {
			_ = ln.Close()
		}
	}()
	for i := 0; i < portProbeCount; i++ {
		ln, err := listen("tcp", "127.0.0.1:0")
		if err != nil {
			return docker.CheckResult{
				Name:        "Ephemeral loopback ports",
				Status:      docker.StatusWarn,
				Detail:      "could not allocate a free loopback port: " + err.Error(),
				Remediation: "DevKit binds ephemeral 127.0.0.1 ports for services; free ports or loosen sandbox restrictions before running `localnet up`.",
			}
		}
		held = append(held, ln)
	}
	return docker.CheckResult{
		Name:   "Ephemeral loopback ports",
		Status: docker.StatusOK,
		Detail: fmt.Sprintf("host can allocate ephemeral loopback ports (probed %d)", portProbeCount),
	}
}
