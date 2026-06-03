package localnet

import (
	"fmt"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
)

// PreflightReportFromDocker adapts the internal docker.Report shape to the
// stable API shape shared by CLI JSON output and Web UI preflight responses.
func PreflightReportFromDocker(dockerRep *docker.Report) types.PreflightReport {
	rep := types.PreflightReport{
		SchemaVersion: types.SchemaVersion,
		OK:            dockerRep.OK(),
		Sections:      []types.PreflightSection{},
	}

	system := types.PreflightSection{Title: "System"}
	resources := types.PreflightSection{Title: "Resources"}
	network := types.PreflightSection{Title: "Network"}
	other := types.PreflightSection{Title: "Other"}

	for _, c := range dockerRep.Results {
		check := types.PreflightCheck{
			Label:  c.Name,
			Result: preflightResultToken(c.Status),
			Detail: c.Detail,
		}
		if c.Remediation != "" && (c.Status == docker.StatusFail || c.Status == docker.StatusWarn) {
			check.Remediation = splitPreflightLines(c.Remediation)
		}
		switch {
		case isResourceCheck(c.Name):
			resources.Checks = append(resources.Checks, check)
		case isNetworkCheck(c.Name):
			network.Checks = append(network.Checks, check)
		case isSystemCheck(c.Name):
			system.Checks = append(system.Checks, check)
		default:
			other.Checks = append(other.Checks, check)
		}
	}

	for _, sec := range []types.PreflightSection{system, resources, network, other} {
		if len(sec.Checks) > 0 {
			rep.Sections = append(rep.Sections, sec)
		}
	}

	failed, warned := 0, 0
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			switch c.Result {
			case "fail":
				failed++
			case "warn":
				warned++
			}
		}
	}
	switch {
	case failed > 0:
		rep.Summary = fmt.Sprintf("%d issue%s · %d warning%s — host is NOT ready",
			failed, preflightPluralS(failed), warned, preflightPluralS(warned))
	case warned > 0:
		rep.Summary = fmt.Sprintf("0 issues · %d warning%s — host is ready (advisories above)",
			warned, preflightPluralS(warned))
	default:
		rep.Summary = "All checks passed — host is ready for `localnet up`"
	}
	return rep
}

// preflightPluralS is a tiny pluraliser inlined here until sibling command
// branches settle on a shared text-formatting helper.
//
// TODO(BIT-141): DRY this up with other localnet plural helpers once the M1
// command branches have merged.
func preflightPluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func preflightResultToken(s docker.Status) string {
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

func splitPreflightLines(s string) []string {
	out := make([]string, 0, 2)
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func isResourceCheck(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "memory") || strings.Contains(n, "disk") ||
		strings.Contains(n, "cpu") || strings.Contains(n, "ram") ||
		strings.Contains(n, "swap")
}

func isNetworkCheck(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "port ") || strings.Contains(n, "ports") || strings.Contains(n, "network") ||
		strings.Contains(n, "dns") || strings.Contains(n, "ipv")
}

func isSystemCheck(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "docker") || strings.Contains(n, "compose") ||
		strings.Contains(n, "os") || strings.Contains(n, "platform") ||
		strings.Contains(n, "host")
}
