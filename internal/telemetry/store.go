package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the counter-payload shape version. Adding/renaming a
// counter bumps it (design decision #18).
const SchemaVersion = 1

// envDir overrides the telemetry state directory in tests so nothing
// touches the real user config dir.
const envDir = "CANTON_DEVKIT_TELEMETRY_DIR"

// telemetryDir is <UserConfigDir>/canton-devkit/telemetry, holding the
// consent config and the weekly counter files. Test-overridable.
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

// currentWeek is the ISO-8601 year-week, e.g. "2026-W22". This is the
// FINEST time granularity we ever record — no sub-week timestamps.
func currentWeek() string {
	y, w := time.Now().UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// WeeklyAggregate is exactly what a weekly file contains and what the
// collector receives — counters merged per week, no per-invocation rows,
// no identifiers, no sub-week timestamps.
type WeeklyAggregate struct {
	SchemaVersion int                       `json:"schema_version"`
	Week          string                    `json:"week"`
	Counters      map[string]map[string]int `json:"counters"`
	// Deferred marks a week whose upload failed once; the uploader retries
	// it at the next window, then drops after a second miss. Not sent.
	Deferred bool `json:"deferred,omitempty"`
}

func weekFilePath(week string) string {
	return filepath.Join(telemetryDir(), week+".json")
}

func loadWeek(week string) (*WeeklyAggregate, error) {
	b, err := os.ReadFile(weekFilePath(week))
	if err != nil {
		if os.IsNotExist(err) {
			return &WeeklyAggregate{SchemaVersion: SchemaVersion, Week: week, Counters: map[string]map[string]int{}}, nil
		}
		return nil, err
	}
	var w WeeklyAggregate
	if err := json.Unmarshal(b, &w); err != nil {
		return &WeeklyAggregate{SchemaVersion: SchemaVersion, Week: week, Counters: map[string]map[string]int{}}, nil
	}
	if w.Counters == nil {
		w.Counters = map[string]map[string]int{}
	}
	return &w, nil
}

func saveWeek(w *WeeklyAggregate) error {
	if err := os.MkdirAll(telemetryDir(), 0o755); err != nil {
		return err
	}
	w.SchemaVersion = SchemaVersion
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(weekFilePath(w.Week), b, 0o644)
}

// mergeWeekly folds a run's counters into the current week's aggregate.
func mergeWeekly(snapshot map[string]map[string]int) error {
	week := currentWeek()
	agg, err := loadWeek(week)
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
	agg.Deferred = false // new activity this week; reset any deferral
	return saveWeek(agg)
}
