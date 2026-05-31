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
	report.Results = append(report.Results,
		platformSupportCheck(goos, goarch),
		ephemeralPortAvailabilityCheck(listen),
	)

	return PreflightReportFromDocker(report), nil
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
