package telemetry

import "testing"

// TestAllowlist_TokenAndUIFeature pins the new adoption counters: the
// CIP-0112 token actions (M3) and the Web UI features (M2) are
// recordable, and a bogus bucket is still dropped.
func TestAllowlist_TokenAndUIFeature(t *testing.T) {
	good := [][2]string{
		{"dpm/token_action", "create"},
		{"dpm/token_action", "mint"},
		{"dpm/token_action", "transfer"},
		{"dpm/token_action", "burn"},
		{"dpm/token_action", "balance"},
		{"dpm/ui_feature", "dar"},
		{"dpm/ui_feature", "explorer"},
		{"dpm/ui_feature", "tokens"},
		{"dpm/ui_feature", "metrics"},
		{"dpm/command", "token"}, // token is now a counted verb
		{"dpm/command_exit", "token/ok"},
	}
	for _, c := range good {
		if !allowed(c[0], c[1]) {
			t.Errorf("allowed(%q,%q) = false, want true", c[0], c[1])
		}
	}
	bad := [][2]string{
		{"dpm/token_action", "destroy"},    // not a real token action
		{"dpm/ui_feature", "secret-panel"}, // not allow-listed
		{"dpm/token_action", ""},
	}
	for _, c := range bad {
		if allowed(c[0], c[1]) {
			t.Errorf("allowed(%q,%q) = true, want false", c[0], c[1])
		}
	}
}

// TestIncOnce_DedupesPerProcess verifies IncOnce records a (chart,bucket)
// exactly once per process even when called repeatedly — the property
// that keeps Web UI polling from inflating dpm/ui_feature.
func TestIncOnce_DedupesPerProcess(t *testing.T) {
	sandbox(t)
	// Reset the process-global dedupe set so this test is independent of
	// any earlier IncOnce in the same test binary.
	onceMu.Lock()
	onceSet = map[string]struct{}{}
	onceMu.Unlock()

	Install(NewSink("dev"))
	for i := 0; i < 5; i++ {
		IncOnce("dpm/ui_feature", "dar")
	}
	IncOnce("dpm/ui_feature", "explorer")
	IncOnce("dpm/ui_feature", "explorer")
	Persist()

	agg, err := PreviewCurrentPeriod()
	if err != nil {
		t.Fatal(err)
	}
	if got := agg.Counters["dpm/ui_feature"]["dar"]; got != 1 {
		t.Errorf("dar counted %d times, want 1 (IncOnce must dedupe)", got)
	}
	if got := agg.Counters["dpm/ui_feature"]["explorer"]; got != 1 {
		t.Errorf("explorer counted %d times, want 1", got)
	}
}
