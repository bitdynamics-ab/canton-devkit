package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

// endpoint is the collector URL (deployed:
// https://canton-devkit-telemetry.bitdynamics.me/v1/counters). Empty by
// default = "ship dark": counters accumulate locally and nothing is sent.
// Baked at release-build time via -ldflags (from the TELEMETRY_ENDPOINT
// Actions variable), or overridden by CANTON_DEVKIT_TELEMETRY_ENDPOINT
// (test seam).
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

// ErrNoCollector is returned by FlushNow when no collector endpoint is
// configured, so the CLI can print an actionable message instead of
// silently doing nothing.
var ErrNoCollector = errors.New("no collector configured — set CANTON_DEVKIT_TELEMETRY_ENDPOINT (or use a release build with a baked-in endpoint)")

// FlushNow uploads EVERY period file — including the CURRENT, still-open
// period — to the collector immediately, removing each on success.
// Returns the number of periods shipped.
//
// This is the on-demand counterpart to tryUpload (which only ships
// completed past periods and runs automatically at process exit). It
// exists so testers and operators don't have to wait for the daily
// window to see counters land. Because the collector SUMS submissions and
// each flushed file is removed locally, repeated flushes within a period
// ship only the delta since the last flush — totals stay correct.
func FlushNow() (int, error) {
	url := resolveEndpoint()
	if url == "" {
		return 0, ErrNoCollector
	}
	entries, err := os.ReadDir(telemetryDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // nothing recorded yet
		}
		return 0, err
	}
	var (
		n        int
		firstErr error
	)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == "config.json" {
			continue
		}
		period := strings.TrimSuffix(name, ".json")
		agg, err := loadPeriod(period)
		if err != nil {
			continue
		}
		if err := uploadPeriod(url, agg); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_ = os.Remove(periodFilePath(period))
		n++
	}
	return n, firstErr
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

// uploadPeriod POSTs one period's counters. The body carries
// schema_version, period, granularity, counters, and (non-CI only) the
// anonymous install_id dedup token — never the internal Deferred flag.
//
// Collector-rollout safety: a collector predating install_id rejects the
// unknown field with a 400 (it decodes with DisallowUnknownFields). To
// avoid a fleet upgrade dropping COUNTERS (not just unique-install dedup),
// a 400 on a token-bearing body triggers exactly ONE retry without the
// token. The counts then land on the old collector; only the dedup signal
// is lost until it's upgraded. Any other status (or a 400 on a body that
// never had a token) is returned as-is.
func uploadPeriod(url string, agg *PeriodAggregate) error {
	base := map[string]any{
		"schema_version": SchemaVersion,
		"period":         agg.Period,
		"granularity":    agg.Granularity,
		"counters":       agg.Counters,
	}
	// Anonymous dedup token (installid.go). Omitted in CI / on CSPRNG
	// failure; the collector treats install_id as optional.
	id := installID()
	payload := base
	if id != "" {
		payload = make(map[string]any, len(base)+1)
		for k, v := range base {
			payload[k] = v
		}
		payload["install_id"] = id
	}

	status, err := postCounters(url, payload)
	if err != nil {
		return err
	}
	if status/100 == 2 {
		return nil
	}
	// Old collector that doesn't know install_id → retry once without it
	// so the counters still ingest during a rollout.
	if status == http.StatusBadRequest && id != "" {
		status, err = postCounters(url, base)
		if err != nil {
			return err
		}
		if status/100 == 2 {
			return nil
		}
	}
	return errBadStatus(status)
}

// postCounters marshals and POSTs one payload, returning the HTTP status
// code (or a transport error). It performs no retries — uploadPeriod owns
// the single install_id fallback retry.
func postCounters(url string, payload map[string]any) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
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
