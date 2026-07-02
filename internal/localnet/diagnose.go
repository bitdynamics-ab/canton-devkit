package localnet

import "strings"

// containerObs is one container's observable state + recent log tail,
// the input to the pure decision layer (diagnoseFromObs) — extracted
// from diagnoseUnhealthy so the matcher logic is testable without a
// docker subprocess. See diagnoseUnhealthy for why the JVM "-Xmx
// exceeds half" warning is NOT a standalone OOM signal.
type containerObs struct {
	Service string // compose service name ("canton", "splice", "postgres", ...)
	State   string // "running" | "restarting" | "exited" | …
	Health  string // "healthy" | "unhealthy" | "starting" | ""
	Status  string // raw "Up 5 minutes (healthy)" or "Restarting (137) 3 seconds ago"
	LogTail string // recent log lines; may be empty
}

// diagnoseFromObs is the pure decision table. Returns one of:
//
//	ErrCodeCantonOOM        — restart loop with kernel SIGKILL signal
//	                           AND the JVM warning OR canton service
//	ErrCodeContainerUnhealthy — generic "unhealthy but not OOM"
//	""                       — no signal, caller picks the fallback
//
// New signals slot in here without touching the IO layer.
func diagnoseFromObs(c containerObs) string {
	if c.Health != "unhealthy" && c.State != "restarting" && c.State != "exited" {
		return ""
	}
	if isRestartingOOM(c.Status) {
		if strings.Contains(c.LogTail, "exceeds half of the container's total memory") {
			return ErrCodeCantonOOM
		}
		// Canton-service restart loop with kernel kill is
		// almost always OOM even without the JVM banner — the
		// kernel may have killed the JVM before it logged.
		if c.Service == "canton" {
			return ErrCodeCantonOOM
		}
	}
	return ""
}

// isRestartingOOM returns true when the compose ps Status string
// indicates a kernel kill. Exit 137 is SIGKILL (typical OOM-kill),
// 143 is SIGTERM (also seen in OOM-killer cgroup notifications).
// The status string format is "Restarting (<code>) <when>" when
// docker is mid-restart loop.
func isRestartingOOM(status string) bool {
	return strings.Contains(status, "Restarting (137)") ||
		strings.Contains(status, "Restarting (143)")
}
