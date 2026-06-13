package telemetry

import (
	"runtime"
	"testing"
)

// TestRecordInstallOnce_FiresOncePerMachine verifies the device-count
// proxy increments dpm/install exactly once no matter how many times it's
// called, and persists the once-flag.
func TestRecordInstallOnce_FiresOncePerMachine(t *testing.T) {
	sandbox(t)
	t.Setenv("CI", "") // ensure non-CI
	Install(NewSink("dev"))

	for i := 0; i < 5; i++ {
		RecordInstallOnce()
	}
	Persist()

	agg, err := PreviewCurrentPeriod()
	if err != nil {
		t.Fatal(err)
	}
	osB := normOS(runtime.GOOS)
	if got := agg.Counters["dpm/install"][osB]; got != 1 {
		t.Errorf("dpm/install[%s] = %d, want 1 (must fire once per machine)", osB, got)
	}
	if !loadConsent().InstallCounted {
		t.Error("InstallCounted flag was not persisted")
	}
}

// TestRecordInstallOnce_SkipsCI verifies a CI host is NOT counted (and the
// flag is left unset, since CI configs are ephemeral) — otherwise every
// CI job would look like a fresh install.
func TestRecordInstallOnce_SkipsCI(t *testing.T) {
	sandbox(t)
	t.Setenv("CI", "true")
	Install(NewSink("dev"))

	RecordInstallOnce()
	Persist()

	agg, _ := PreviewCurrentPeriod()
	if _, ok := agg.Counters["dpm/install"]; ok {
		t.Error("dpm/install was recorded under CI — must be gated to non-CI")
	}
	if loadConsent().InstallCounted {
		t.Error("InstallCounted should stay false under CI (ephemeral host)")
	}
}

// TestAllowlist_Install pins that the install buckets are recordable.
func TestAllowlist_Install(t *testing.T) {
	for _, os := range []string{"linux", "darwin", "windows"} {
		if !allowed("dpm/install", os) {
			t.Errorf("allowed(dpm/install, %s) = false, want true", os)
		}
	}
	if allowed("dpm/install", "plan9") {
		t.Error("non-allow-listed install bucket accepted")
	}
}
