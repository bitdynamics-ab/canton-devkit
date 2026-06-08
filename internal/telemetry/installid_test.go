package telemetry

import (
	"regexp"
	"testing"
)

var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// withCleanEnv points the telemetry dir at a temp dir and clears the CI
// env so installID() is not gated off. Returns nothing; t.Setenv auto-
// restores.
func withCleanEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envDir, t.TempDir())
	// Defang the CI detector: clear the common signals so the test host's
	// own CI environment (if any) doesn't suppress the token.
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD"} {
		t.Setenv(k, "")
	}
}

func TestInstallID_MintedOnceAndStable(t *testing.T) {
	withCleanEnv(t)
	id1 := installID()
	if id1 == "" {
		t.Fatal("installID() returned empty on a clean non-CI host")
	}
	if !uuidV4Re.MatchString(id1) {
		t.Errorf("installID() = %q, not a UUIDv4", id1)
	}
	// Stable across calls — persisted in config, not re-minted.
	if id2 := installID(); id2 != id1 {
		t.Errorf("installID() not stable: %q then %q", id1, id2)
	}
	// And it actually landed in the consent file.
	if c := loadConsent(); c.InstallID != id1 {
		t.Errorf("InstallID not persisted: config has %q, want %q", c.InstallID, id1)
	}
}

func TestInstallID_SuppressedInCI(t *testing.T) {
	t.Setenv(envDir, t.TempDir())
	t.Setenv("CI", "true")
	if id := installID(); id != "" {
		t.Errorf("installID() = %q in CI, want empty (CI hosts must not mint tokens)", id)
	}
	if c := loadConsent(); c.InstallID != "" {
		t.Errorf("CI run persisted a token: %q", c.InstallID)
	}
}

func TestResetInstallID_RotatesToken(t *testing.T) {
	withCleanEnv(t)
	id1 := installID()
	if id1 == "" {
		t.Fatal("setup: empty id")
	}
	if err := ResetInstallID(); err != nil {
		t.Fatalf("ResetInstallID: %v", err)
	}
	if c := loadConsent(); c.InstallID != "" {
		t.Errorf("token not cleared by reset: %q", c.InstallID)
	}
	id2 := installID()
	if id2 == "" || id2 == id1 {
		t.Errorf("reset did not mint a fresh token: before=%q after=%q", id1, id2)
	}
}

func TestSetEnabledOff_ClearsInstallID(t *testing.T) {
	withCleanEnv(t)
	id := installID()
	if id == "" {
		t.Fatal("setup: empty id")
	}
	// Turning telemetry off must clear the token (privacy promise in docs).
	if err := SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if c := loadConsent(); c.InstallID != "" {
		t.Errorf("telemetry off did not clear install id: %q", c.InstallID)
	}
	// Re-enabling mints a fresh token, not the old one.
	if err := SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if id2 := installID(); id2 == "" || id2 == id {
		t.Errorf("re-enable reused or dropped the token: before=%q after=%q", id, id2)
	}
}

func TestNewInstallID_DistinctAndShaped(t *testing.T) {
	a, b := newInstallID(), newInstallID()
	if a == "" || b == "" {
		t.Fatal("newInstallID returned empty")
	}
	if a == b {
		t.Errorf("two mints collided: %q", a)
	}
	for _, id := range []string{a, b} {
		if !uuidV4Re.MatchString(id) {
			t.Errorf("%q is not a UUIDv4", id)
		}
	}
}
