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

// tryUpload uploads any COMPLETED past-period files (every file whose key
// isn't the current period), one POST each. The current period keeps
// accumulating and is never sent while still open. On success the file is
// deleted; on the first failure it is marked Deferred (retried next
// window); on a second failure it is dropped. Best-effort and silent.
// No-op when no endpoint is configured.
func tryUpload() {
	url := resolveEndpoint()
	if url == "" {
		return // ship-dark
	}
	cur := currentPeriod()
	for _, period := range pendingPeriodFiles(cur) {
		agg, err := loadPeriod(period)
		if err != nil {
			continue
		}
		if uploadPeriod(url, agg) == nil {
			_ = os.Remove(periodFilePath(period))
			continue
		}
		// Upload failed.
		if agg.Deferred {
			_ = os.Remove(periodFilePath(period)) // second miss → drop
		} else {
			agg.Deferred = true
			_ = savePeriod(agg)
		}
	}
}

// pendingPeriodFiles lists period identifiers with a file on disk other
// than the current period. We only ever write the current period's file,
// so any other file is by definition a completed past period ready to
// ship — comparing by inequality (not lexicographic order) means a
// daily↔weekly granularity switch never strands a file whose key format
// sorts unexpectedly against the new current key.
func pendingPeriodFiles(cur string) []string {
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
		period := strings.TrimSuffix(name, ".json")
		if period != cur {
			out = append(out, period)
		}
	}
	return out
}

// uploadPeriod POSTs one period's counters. The body carries ONLY
// schema_version, period, granularity, and counters — never the internal
// Deferred flag.
func uploadPeriod(url string, agg *PeriodAggregate) error {
	body, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"period":         agg.Period,
		"granularity":    agg.Granularity,
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

// CurrentPeriodFile returns the path of this period's local counter file,
// for `telemetry preview`.
func CurrentPeriodFile() string { return periodFilePath(currentPeriod()) }

// PreviewCurrentPeriod returns this period's aggregate (what's queued
// locally) for `telemetry preview` / `status`.
func PreviewCurrentPeriod() (*PeriodAggregate, error) { return loadPeriod(currentPeriod()) }
