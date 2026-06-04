package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// channel is the release channel. main() sets it from its own
// -ldflags "-X main.channel=stable|nightly" build var via SetChannel.
// Defaults to "dev" for a local `go build` (design decision #15).
var channel = "dev"

// SetChannel records the release channel (called once by main at start).
func SetChannel(c string) {
	if c != "" {
		channel = c
	}
}

// Channel returns the build channel, normalized to the allow-list.
func Channel() string {
	switch channel {
	case "stable", "nightly":
		return channel
	default:
		return "dev"
	}
}

// RecordContext increments the per-invocation context counters that are
// cheap to determine (no Docker calls): channel, os, arch, ci, llm_agent.
// docker_engine / compose_version_bucket / doctor_fail are emitted by the
// code that already computes them (preflight / doctor), not here.
func RecordContext() {
	Inc("dpm/channel", Channel())
	Inc("dpm/os", normOS(runtime.GOOS))
	Inc("dpm/arch", normArch(runtime.GOARCH))
	Inc("dpm/ci", boolStr(detectCI()))
	Inc("dpm/llm_agent", detectAgent())
}

func normOS(goos string) string {
	switch goos {
	case "linux", "darwin", "windows":
		return goos
	default:
		return "" // dropped by the allow-list
	}
}

func normArch(goarch string) string {
	switch goarch {
	case "amd64", "arm64":
		return goarch
	default:
		return ""
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// detectCI reports whether we're running in CI, from the de-facto-standard
// CI env var (set by GitHub Actions, GitLab, CircleCI, etc.).
func detectCI() bool {
	v := os.Getenv("CI")
	return v != "" && v != "0" && v != "false"
}

// detectAgent buckets the running LLM coding agent from well-known env
// vars. Unknown/none → "none" (never "other"), per design decision #3.
func detectAgent() string {
	switch {
	case os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE") != "":
		return "claude"
	case os.Getenv("CURSOR_TRACE_ID") != "" || os.Getenv("CURSOR") != "":
		return "cursor"
	case os.Getenv("GITHUB_COPILOT") != "" || os.Getenv("COPILOT_AGENT") != "":
		return "copilot"
	case os.Getenv("GEMINI_CLI") != "" || os.Getenv("GEMINI_AGENT") != "":
		return "gemini"
	default:
		return "none"
	}
}

// ComposeBucket maps a `docker compose version --short` string to the
// allow-listed bucket (decision #3, matching the preflight buckets).
// Empty version → "" (dropped).
func ComposeBucket(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	maj, min, ok := majorMinor(v)
	if !ok || maj < 2 {
		return ""
	}
	switch {
	case min < 20:
		return "v2.20-"
	case min < 28:
		return "v2.20-v2.27"
	default:
		return "v2.28+"
	}
}

func majorMinor(v string) (int, int, bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, e1 := strconv.Atoi(parts[0])
	min, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

// NormEngine clamps a detected docker engine to the allow-list; unknown →
// "other", empty → "" (dropped).
func NormEngine(e string) string {
	switch e {
	case "docker", "colima", "orbstack", "podman", "other":
		return e
	case "":
		return ""
	default:
		return "other"
	}
}

// Slug lowercases and hyphenates a doctor check label into a stable
// bucket id for dpm/doctor_fail (e.g. "Docker Compose v2" → "docker-compose-v2").
func Slug(label string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// debugDump writes the JSON that WOULD be sent for the run's counters to
// stderr and skips the network (DPM_TELEMETRY_DEBUG=1, decision #10).
func debugDump(period string, counters map[string]map[string]int) {
	b, _ := json.MarshalIndent(map[string]any{
		"schema_version": SchemaVersion,
		"period":         period,
		"granularity":    string(aggregationPeriod),
		"counters":       counters,
	}, "", "  ")
	_, _ = fmt.Fprintf(os.Stderr, "[telemetry debug] would send:\n%s\n", b)
}
