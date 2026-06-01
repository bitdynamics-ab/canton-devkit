package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// endpoint is the collector URL (design: telemetry.canton-devkit.dev/v1/
// counters). Empty by default = "ship dark": counters accumulate locally
// and nothing is sent. Baked at release-build time via -ldflags, or
// overridden by CANTON_DEVKIT_TELEMETRY_ENDPOINT (test seam).
var endpoint = ""

const envEndpoint = "CANTON_DEVKIT_TELEMETRY_ENDPOINT"

const uploadTimeout = 2 * time.Second

func resolveEndpoint() string {
	if e := os.Getenv(envEndpoint); e != "" {
		return e
	}
	return endpoint
}

// tryWeeklyUpload uploads any COMPLETED past-week files (week < current),
// one POST each. The current week keeps accumulating and is never sent
// while still open. On success the file is deleted; on the first failure
// it is marked Deferred (retried next window); on a second failure it is
// dropped. Best-effort and silent. No-op when no endpoint is configured.
func tryWeeklyUpload() {
	url := resolveEndpoint()
	if url == "" {
		return // ship-dark
	}
	cur := currentWeek()
	for _, week := range pastWeekFiles(cur) {
		agg, err := loadWeek(week)
		if err != nil {
			continue
		}
		if uploadWeek(url, agg) == nil {
			_ = os.Remove(weekFilePath(week))
			continue
		}
		// Upload failed.
		if agg.Deferred {
			_ = os.Remove(weekFilePath(week)) // second miss → drop
		} else {
			agg.Deferred = true
			_ = saveWeek(agg)
		}
	}
}

// pastWeekFiles lists week identifiers with a file on disk that are
// strictly before `cur` (ISO week strings sort lexicographically).
func pastWeekFiles(cur string) []string {
	entries, err := os.ReadDir(telemetryDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == "config.json" {
			continue
		}
		week := strings.TrimSuffix(name, ".json")
		if week < cur {
			out = append(out, week)
		}
	}
	return out
}

// uploadWeek POSTs one week's counters. The body carries ONLY
// schema_version, week, and counters — never the internal Deferred flag.
func uploadWeek(url string, agg *WeeklyAggregate) error {
	body, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"week":           agg.Week,
		"counters":       agg.Counters,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req) // no retries inside the attempt
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return errBadStatus(resp.StatusCode)
	}
	return nil
}

type errBadStatus int

func (e errBadStatus) Error() string { return "collector returned non-2xx" }

// EffectiveEndpoint is the resolved collector URL ("" = ship-dark), for
// `telemetry status`.
func EffectiveEndpoint() string { return resolveEndpoint() }

// CurrentWeekFile returns the path of this week's local counter file, for
// `telemetry preview`.
func CurrentWeekFile() string { return weekFilePath(currentWeek()) }

// PreviewCurrentWeek returns this week's aggregate (what's queued
// locally) for `telemetry preview` / `status`.
func PreviewCurrentWeek() (*WeeklyAggregate, error) { return loadWeek(currentWeek()) }
