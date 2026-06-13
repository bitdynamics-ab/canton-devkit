package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is the counter-payload shape version. Adding/renaming a
// counter bumps it. v2 renamed the period key from "week" to "period" and
// added the self-describing "granularity" tag when aggregation moved from
// weekly to daily.
const SchemaVersion = 2

// envDir overrides the telemetry state directory in tests so nothing
// touches the real user config dir.
const envDir = "CANTON_DEVKIT_TELEMETRY_DIR"

// periodKind selects how finely counters are bucketed in time. This is
// the SINGLE switch point: flip aggregationPeriod to periodWeekly and
// currentPeriod(), the file names, and the payload's "granularity" tag
// all follow. No other code changes.
type periodKind string

const (
	periodDaily  periodKind = "daily"
	periodWeekly periodKind = "weekly"
)

// aggregationPeriod is the active bucket granularity. Daily for now;
// switch to periodWeekly to roll counters up weekly again.
const aggregationPeriod = periodDaily

// telemetryDir is <UserConfigDir>/canton-devkit/telemetry, holding the
// consent config and the per-period counter files. Test-overridable.
func telemetryDir() string {
	if d := os.Getenv(envDir); d != "" {
		return d
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".", ".config", "canton-devkit", "telemetry")
	}
	return filepath.Join(cfg, "canton-devkit", "telemetry")
}

// currentPeriod is the bucket key for "now" in UTC: a calendar date
// "2026-06-04" when daily, or an ISO-8601 year-week "2026-W22" when
// weekly. This is the FINEST time granularity we ever record — never a
// sub-period timestamp. Both forms sort lexicographically within a
// granularity; the uploader treats any file that isn't the current key
// as a completed past period, so a granularity switch never strands old
// files regardless of cross-format ordering.
func currentPeriod() string {
	now := time.Now().UTC()
	if aggregationPeriod == periodWeekly {
		y, w := now.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	}
	return now.Format("2006-01-02")
}

// PeriodAggregate is exactly what a period file contains and what the
// collector receives — counters merged per period, no per-invocation
// rows, no identifiers, no sub-period timestamps. Granularity tags the
// bucket size ("daily" / "weekly") so the collector can interpret Period
// without guessing from its format.
type PeriodAggregate struct {
	SchemaVersion int                       `json:"schema_version"`
	Period        string                    `json:"period"`
	Granularity   string                    `json:"granularity"`
	Counters      map[string]map[string]int `json:"counters"`
	// Deferred marks a period whose upload failed once; the uploader
	// retries it at the next window, then drops after a second miss. Not
	// sent.
	Deferred bool `json:"deferred,omitempty"`
}

func periodFilePath(period string) string {
	return filepath.Join(telemetryDir(), period+".json")
}

// inferGranularity recovers the bucket size from a period key's shape so a
// pre-v2 file (no Granularity field) lands on the correct tag — ISO-week
// keys look like "YYYY-Wnn" and dates look like "YYYY-MM-DD". Without this,
// an upgrade-time leftover weekly file would be reported to the collector
// (and, on a deferred re-save, persisted) tagged as the current
// aggregationPeriod, which is exactly the cross-format inconsistency the
// granularity field was added to prevent.
func inferGranularity(period string) periodKind {
	if strings.Contains(period, "-W") {
		return periodWeekly
	}
	return periodDaily
}

func newPeriodAggregate(period string) *PeriodAggregate {
	return &PeriodAggregate{
		SchemaVersion: SchemaVersion,
		Period:        period,
		Granularity:   string(aggregationPeriod),
		Counters:      map[string]map[string]int{},
	}
}

func loadPeriod(period string) (*PeriodAggregate, error) {
	b, err := os.ReadFile(periodFilePath(period))
	if err != nil {
		if os.IsNotExist(err) {
			return newPeriodAggregate(period), nil
		}
		return nil, err
	}
	var w PeriodAggregate
	if err := json.Unmarshal(b, &w); err != nil {
		return newPeriodAggregate(period), nil
	}
	// The filename is authoritative for the period key. v1 files used a
	// "week" JSON field that doesn't bind to v2's "period" tag; trusting
	// the filename means a v1→v2 upgrade doesn't need a field-level
	// migration step and keeps Period consistent regardless of what's
	// inside the body.
	w.Period = period
	if w.Counters == nil {
		w.Counters = map[string]map[string]int{}
	}
	if w.Granularity == "" {
		// v1 files had no Granularity field; infer from the key shape so
		// a leftover weekly file uploaded after the daily switch ships
		// the correct tag (and a Deferred re-save doesn't bake the wrong
		// one into disk).
		w.Granularity = string(inferGranularity(w.Period))
	}
	return &w, nil
}

func savePeriod(w *PeriodAggregate) error {
	if err := os.MkdirAll(telemetryDir(), 0o755); err != nil {
		return err
	}
	w.SchemaVersion = SchemaVersion
	if w.Granularity == "" {
		// Same migration safety as loadPeriod: infer from key shape so a
		// missing-Granularity in-memory aggregate (e.g. one a caller
		// hand-built) doesn't get persisted with the wrong tag.
		w.Granularity = string(inferGranularity(w.Period))
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(periodFilePath(w.Period), b, 0o644)
}

// mergePeriod folds a run's counters into the current period's aggregate.
func mergePeriod(snapshot map[string]map[string]int) error {
	period := currentPeriod()
	agg, err := loadPeriod(period)
	if err != nil {
		return err
	}
	for chart, buckets := range snapshot {
		dst := agg.Counters[chart]
		if dst == nil {
			dst = map[string]int{}
			agg.Counters[chart] = dst
		}
		for bucket, n := range buckets {
			dst[bucket] += n
		}
	}
	agg.Deferred = false // new activity this period; reset any deferral
	return savePeriod(agg)
}
