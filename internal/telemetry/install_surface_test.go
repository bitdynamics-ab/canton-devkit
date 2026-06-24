package telemetry

import "testing"

func TestRecordInstallSurfaceOnce_FiresOncePerSurface(t *testing.T) {
	sandbox(t)
	t.Setenv("CI", "")
	Install(NewSink("dev"))

	for i := 0; i < 4; i++ {
		if got := RecordInstallSurfaceOnce("apt"); i == 0 && !got {
			t.Fatal("first APT install surface ping was dropped")
		} else if i > 0 && got {
			t.Fatal("duplicate APT install surface ping was not deduped")
		}
	}
	Persist()

	agg, err := PreviewCurrentPeriod()
	if err != nil {
		t.Fatal(err)
	}
	if got := agg.Counters["dpm/install_surface"]["apt"]; got != 1 {
		t.Fatalf("dpm/install_surface[apt] = %d, want 1", got)
	}
	if !loadConsent().InstallSurfaceCounted["apt"] {
		t.Fatal("APT install surface marker was not persisted")
	}
}

func TestRecordInstallSurfaceOnce_SkipsCI(t *testing.T) {
	sandbox(t)
	t.Setenv("CI", "true")
	Install(NewSink("dev"))

	if got := RecordInstallSurfaceOnce("apt"); got {
		t.Fatal("CI install surface ping should be suppressed")
	}
	Persist()

	agg, err := PreviewCurrentPeriod()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := agg.Counters["dpm/install_surface"]; ok {
		t.Fatal("dpm/install_surface was recorded under CI")
	}
}

func TestAllowlist_InstallSurface(t *testing.T) {
	if !allowed("dpm/install_surface", "apt") {
		t.Fatal("apt install surface should be allow-listed")
	}
	if allowed("dpm/install_surface", "homebrew") {
		t.Fatal("unexpected install surface bucket accepted")
	}
}
