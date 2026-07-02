package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func sandbox(t *testing.T) {
	t.Helper()
	t.Setenv(envDir, t.TempDir())
	t.Setenv(envDoNotTrack, "")
	t.Setenv(envToggle, "")
	t.Setenv(envEndpoint, "")
	t.Setenv(envDebug, "")
	Install(noopSink{}) // reset global sink between tests
}

func TestEnabledByDefault_OptOut(t *testing.T) {
	sandbox(t)
	on, src := Enabled()
	if !on || src != SourceDefault {
		t.Fatalf("default should be ON via SourceDefault, got %v / %s", on, src)
	}
}

func TestPrecedence(t *testing.T) {
	sandbox(t)
	// config off
	if err := SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if on, src := Enabled(); on || src != SourceConfig {
		t.Errorf("config off: got %v/%s", on, src)
	}
	// env toggle on beats config off
	t.Setenv(envToggle, "on")
	if on, src := Enabled(); !on || src != SourceEnvToggle {
		t.Errorf("env on should win over config: %v/%s", on, src)
	}
	// DO_NOT_TRACK beats everything
	t.Setenv(envDoNotTrack, "1")
	if on, src := Enabled(); on || src != SourceDoNotTrack {
		t.Errorf("DO_NOT_TRACK should win: %v/%s", on, src)
	}
}

func TestInc_PeriodAggregate_NoIDsNoTimestamps(t *testing.T) {
	sandbox(t)
	Install(NewSink("dev"))
	Inc("dpm/command", "up")
	Inc("dpm/command", "up")
	Inc("dpm/command", "down")
	Inc("dpm/os", "darwin")
	Inc("dpm/nonsense", "x") // not allow-listed → dropped
	Persist()

	agg, err := PreviewCurrentPeriod()
	if err != nil {
		t.Fatal(err)
	}
	if agg.Counters["dpm/command"]["up"] != 2 || agg.Counters["dpm/command"]["down"] != 1 {
		t.Errorf("command counts wrong: %+v", agg.Counters["dpm/command"])
	}
	if _, ok := agg.Counters["dpm/nonsense"]; ok {
		t.Error("non-allow-listed counter leaked through")
	}
	// Zero-PII / no-identifier guard: the serialized period file must not
	// contain any id/sub-period-timestamp/pii token.
	b, _ := json.Marshal(agg)
	low := strings.ToLower(string(b))
	for _, bad := range []string{"install", "uuid", "machine", "\"ts\"", "timestamp", "::", "/users/", "party", "jwt"} {
		if strings.Contains(low, bad) {
			t.Errorf("period file leaked %q: %s", bad, b)
		}
	}
	// The period file is tagged with the active granularity (daily).
	if agg.Granularity != "daily" {
		t.Errorf("granularity = %q, want daily", agg.Granularity)
	}
}

func TestInc_DroppedWhenDisabled(t *testing.T) {
	sandbox(t)
	_ = SetEnabled(false)
	// App installs the real sink only when enabled; emulate that.
	if IsEnabled() {
		t.Fatal("should be disabled")
	}
	// With the no-op sink installed (sandbox default), Inc writes nothing.
	Inc("dpm/command", "up")
	Persist()
	agg, _ := PreviewCurrentPeriod()
	if len(agg.Counters) != 0 {
		t.Errorf("disabled telemetry must not record, got %+v", agg.Counters)
	}
}

func TestUpload_PastPeriodAndDrop(t *testing.T) {
	sandbox(t)
	var mu sync.Mutex
	var got map[string]any
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv(envEndpoint, srv.URL)

	// Seed a PAST period file directly. Use a leftover WEEKLY-format key
	// ("2000-W01") on purpose: any file that isn't the current (daily) key
	// must still drain, even though "2000-W01" sorts AFTER a "2026-..."
	// daily key — a lexicographic `< cur` predicate would strand it forever.
	past := &PeriodAggregate{SchemaVersion: SchemaVersion, Period: "2000-W01", Granularity: "weekly",
		Counters: map[string]map[string]int{"dpm/command": {"up": 3}}}
	if err := savePeriod(past); err != nil {
		t.Fatal(err)
	}

	// First attempt fails → file kept, marked deferred.
	tryUpload()
	if _, err := os.Stat(periodFilePath("2000-W01")); err != nil {
		t.Fatal("file should survive first failure")
	}
	w1, _ := loadPeriod("2000-W01")
	if !w1.Deferred {
		t.Error("should be marked deferred after first failure")
	}
	// Second attempt fails → dropped.
	tryUpload()
	if _, err := os.Stat(periodFilePath("2000-W01")); !os.IsNotExist(err) {
		t.Error("file should be dropped after second failure")
	}

	// Now a successful upload clears the file.
	if err := savePeriod(past); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	tryUpload()
	mu.Lock()
	evPeriod, _ := got["period"].(string)
	evGran, _ := got["granularity"].(string)
	mu.Unlock()
	if evPeriod != "2000-W01" {
		t.Errorf("collector got period %q, want 2000-W01", evPeriod)
	}
	if evGran != "weekly" {
		t.Errorf("collector got granularity %q, want weekly (the file's own tag)", evGran)
	}
	if _, err := os.Stat(periodFilePath("2000-W01")); !os.IsNotExist(err) {
		t.Error("file should be removed after successful upload")
	}
}

// TestUpload_V1UpgradeFile_TagsWeeklyByKeyShape regression-guards the
// v1→v2 migration boundary: a file written by a pre-v2 binary has the
// v1 shape (a "week" key, no "granularity" field). When the v2 binary
// loads it for upload, the granularity tag MUST be recovered from the
// key shape ("2000-W01" → weekly), not defaulted to the current
// aggregationPeriod — otherwise the collector receives an ISO-week
// period stamped as "daily", or a Deferred re-save persists the wrong
// tag back to disk.
func TestUpload_V1UpgradeFile_TagsWeeklyByKeyShape(t *testing.T) {
	sandbox(t)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv(envEndpoint, srv.URL)

	// Hand-write the v1 shape — savePeriod would now inject granularity,
	// so we bypass it to authentically simulate a pre-v2 file on disk.
	if err := os.MkdirAll(telemetryDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := []byte(`{
  "schema_version": 1,
  "week": "2000-W01",
  "counters": {"dpm/command": {"up": 3}}
}`)
	if err := os.WriteFile(periodFilePath("2000-W01"), v1, 0o644); err != nil {
		t.Fatal(err)
	}

	tryUpload()

	evPeriod, _ := got["period"].(string)
	evGran, _ := got["granularity"].(string)
	if evPeriod != "2000-W01" {
		t.Errorf("collector got period %q, want 2000-W01", evPeriod)
	}
	if evGran != "weekly" {
		t.Errorf("collector got granularity %q, want weekly (inferred from -W key shape)", evGran)
	}
	if _, err := os.Stat(periodFilePath("2000-W01")); !os.IsNotExist(err) {
		t.Error("file should be removed after successful upload")
	}
}

// TestUpload_V1UpgradeFile_DeferredReSavePreservesWeeklyTag covers the
// failure path of the same migration: when the v1 upload fails, the
// Deferred re-save MUST stamp the inferred weekly tag onto disk so the
// next attempt still ships the correct granularity.
func TestUpload_V1UpgradeFile_DeferredReSavePreservesWeeklyTag(t *testing.T) {
	sandbox(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv(envEndpoint, srv.URL)

	if err := os.MkdirAll(telemetryDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := []byte(`{"schema_version":1,"week":"2000-W01","counters":{"dpm/command":{"up":3}}}`)
	if err := os.WriteFile(periodFilePath("2000-W01"), v1, 0o644); err != nil {
		t.Fatal(err)
	}

	tryUpload() // fails → file marked Deferred, re-saved with inferred granularity

	reloaded, err := loadPeriod("2000-W01")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Granularity != "weekly" {
		t.Errorf("after Deferred re-save granularity = %q, want weekly", reloaded.Granularity)
	}
	if !reloaded.Deferred {
		t.Error("should be marked Deferred after first failure")
	}
}

func TestComposeBucket(t *testing.T) {
	cases := map[string]string{"2.19.1": "v2.20-", "v2.20.0": "v2.20-v2.27", "2.27.9": "v2.20-v2.27", "2.28.0": "v2.28+", "2.35.1": "v2.28+", "": "", "garbage": ""}
	for in, want := range cases {
		if got := ComposeBucket(in); got != want {
			t.Errorf("ComposeBucket(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNormEngineAndSlug(t *testing.T) {
	if NormEngine("rancher") != "other" || NormEngine("colima") != "colima" || NormEngine("") != "" {
		t.Error("NormEngine clamp wrong")
	}
	if Slug("Docker Compose v2") != "docker-compose-v2" {
		t.Errorf("Slug = %q", Slug("Docker Compose v2"))
	}
}
