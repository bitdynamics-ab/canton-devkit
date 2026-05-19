package docker

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func checkDockerCLI() CheckResult {
	if _, err := exec.LookPath("docker"); err != nil {
		return CheckResult{
			Name:        "Docker CLI",
			Status:      StatusFail,
			Detail:      "`docker` not found in PATH",
			Remediation: remediationDockerInstall(),
		}
	}
	return CheckResult{Name: "Docker CLI", Status: StatusOK}
}

func checkDockerDaemon(ctx context.Context) (CheckResult, string) {
	ctx, cancel := context.WithTimeout(ctx, preflightCheckTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		combined := strings.ToLower(string(out))
		remediation := remediationDaemonNotRunning()
		if strings.Contains(combined, "permission denied") {
			remediation = remediationDockerPermissions()
		}
		return CheckResult{
			Name:        "Docker daemon",
			Status:      StatusFail,
			Detail:      firstLine(string(out)),
			Remediation: remediation,
		}, ""
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return CheckResult{
			Name:        "Docker daemon",
			Status:      StatusFail,
			Detail:      "daemon returned empty version",
			Remediation: remediationDaemonNotRunning(),
		}, ""
	}
	return CheckResult{
		Name:   "Docker daemon",
		Status: StatusOK,
		Detail: "v" + version,
	}, version
}

func checkComposeV2(ctx context.Context) (CheckResult, string) {
	ctx, cancel := context.WithTimeout(ctx, preflightCheckTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "compose", "version", "--short").CombinedOutput()
	if err != nil {
		return CheckResult{
			Name:        "Docker Compose v2",
			Status:      StatusFail,
			Detail:      firstLine(string(out)),
			Remediation: remediationComposeMissing(),
		}, ""
	}
	version := strings.TrimSpace(string(out))
	major, err := parseMajor(version)
	if err != nil || major < 2 {
		return CheckResult{
			Name:        "Docker Compose v2",
			Status:      StatusFail,
			Detail:      "found version " + version,
			Remediation: remediationComposeMissing(),
		}, version
	}
	return CheckResult{
		Name:   "Docker Compose v2",
		Status: StatusOK,
		Detail: "v" + strings.TrimPrefix(version, "v"),
	}, version
}

func parseMajor(version string) (int, error) {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	return strconv.Atoi(v)
}

func checkDockerMemory(ctx context.Context, minBytes uint64) CheckResult {
	if minBytes == 0 {
		return CheckResult{Name: "Docker memory", Status: StatusSkipped, Detail: "no minimum set"}
	}
	ctx, cancel := context.WithTimeout(ctx, preflightCheckTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.MemTotal}}").Output()
	if err != nil {
		return CheckResult{
			Name:        "Docker memory",
			Status:      StatusWarn,
			Detail:      "could not query daemon memory",
			Remediation: remediationMemoryUnknown(),
		}
	}
	raw := strings.TrimSpace(string(out))
	mem, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || mem == 0 {
		return CheckResult{
			Name:        "Docker memory",
			Status:      StatusWarn,
			Detail:      "unparseable MemTotal: " + raw,
			Remediation: remediationMemoryUnknown(),
		}
	}
	if mem < minBytes {
		return CheckResult{
			Name:        "Docker memory",
			Status:      StatusFail,
			Detail:      fmt.Sprintf("%s available, %s recommended", humanBytes(mem), humanBytes(minBytes)),
			Remediation: remediationMemoryLow(),
		}
	}
	return CheckResult{
		Name:   "Docker memory",
		Status: StatusOK,
		Detail: humanBytes(mem) + " available",
	}
}

func checkDiskSpace(path string, minBytes uint64) CheckResult {
	avail, err := availableDiskBytes(path)
	if err != nil {
		return CheckResult{
			Name:        "Disk space",
			Status:      StatusWarn,
			Detail:      "could not stat " + path + ": " + err.Error(),
			Remediation: remediationDiskUnknown(),
		}
	}
	if avail < minBytes {
		return CheckResult{
			Name:        "Disk space",
			Status:      StatusFail,
			Detail:      fmt.Sprintf("%s free at %s, %s recommended", humanBytes(avail), path, humanBytes(minBytes)),
			Remediation: remediationDiskLow(),
		}
	}
	return CheckResult{
		Name:   "Disk space",
		Status: StatusOK,
		Detail: humanBytes(avail) + " free at " + path,
	}
}

func checkHostPrereqs() CheckResult {
	name := "Host prerequisites (" + runtime.GOOS + ")"
	switch runtime.GOOS {
	case "linux":
		return CheckResult{Name: name, Status: StatusOK, Detail: "ensure user is in `docker` group if daemon access fails"}
	case "darwin", "windows":
		return CheckResult{Name: name, Status: StatusOK, Detail: "Docker Desktop must be running"}
	default:
		return CheckResult{Name: name, Status: StatusWarn, Detail: "unsupported OS"}
	}
}

// --- helpers --------------------------------------------------------------

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func humanBytes(n uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.0f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(KB))
	}
	return fmt.Sprintf("%d B", n)
}
