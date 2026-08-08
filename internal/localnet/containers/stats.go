package containers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Stat is one container's live resource sample from `docker stats`.
// CPU/memory come straight from docker, so they're available whenever the
// instance is running — independent of the observability (Prometheus)
// profile that gates ledger metrics.
type Stat struct {
	Name     string  // docker container name (e.g. "minttest-canton-1")
	CPUPct   float64 // percent of one core (docker's CPUPerc; sums can exceed 100)
	MemBytes int64   // memory in use
	MemLimit int64   // memory limit ("granted")
}

// Stats runs `docker stats --no-stream` for the named containers and returns
// a per-name sample. Empty names → empty map, no docker call. Best-effort by
// contract: callers treat an error as "no resource data" and render nothing
// rather than failing the surrounding response.
func Stats(ctx context.Context, names []string) (map[string]Stat, error) {
	if len(names) == 0 {
		return map[string]Stat{}, nil
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, names...)
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w", err)
	}
	return parseStats(out)
}

// dockerStatsLine is the subset of `docker stats --format "{{json .}}"` we read.
type dockerStatsLine struct {
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`  // "12.34%"
	MemUsage string `json:"MemUsage"` // "740MiB / 2GiB"
}

// parseStats parses `docker stats --format "{{json .}}"` output — one JSON
// object per line. An unparseable line is skipped rather than failing the
// whole sample.
func parseStats(raw []byte) (map[string]Stat, error) {
	out := map[string]Stat{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var d dockerStatsLine
		if err := json.Unmarshal(line, &d); err != nil {
			continue
		}
		used, limit := parseMemUsage(d.MemUsage)
		out[d.Name] = Stat{
			Name:     d.Name,
			CPUPct:   parsePercent(d.CPUPerc),
			MemBytes: used,
			MemLimit: limit,
		}
	}
	return out, sc.Err()
}

func parsePercent(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseMemUsage parses docker's "used / limit" memory string
// ("740MiB / 2GiB") into bytes; (0,0) on any parse trouble.
func parseMemUsage(s string) (used, limit int64) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return parseSize(parts[0]), parseSize(parts[1])
}

// parseSize parses a docker human size ("740MiB", "2GiB", "512kB", "1.5GB",
// "0B") into bytes. Docker stats uses IEC units; treat SI suffixes as IEC
// too since a dev-tool KPI doesn't need SI/IEC precision.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	num, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	var mult float64
	switch strings.ToLower(strings.TrimSpace(s[i:])) {
	case "b", "":
		mult = 1
	case "kb", "kib":
		mult = 1 << 10
	case "mb", "mib":
		mult = 1 << 20
	case "gb", "gib":
		mult = 1 << 30
	case "tb", "tib":
		mult = 1 << 40
	default:
		// Unknown unit (e.g. PiB) — 0 rather than silently mis-scaling.
		return 0
	}
	return int64(num * mult)
}
