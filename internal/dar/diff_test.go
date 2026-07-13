package dar

import (
	"strings"
	"testing"
)

// Tiny in-memory Info fixtures so we can exercise Compare without a
// real DAR file. Build helpers keep the test cases declarative.

func infoFixture(path, mainName, mainVersion, mainPkgID, lfMajor, lfMinor string, deps ...*PackageMeta) *Info {
	main := &PackageMeta{
		PackageID: mainPkgID,
		Name:      mainName,
		Version:   mainVersion,
		LFMajor:   lfMajor,
		LFMinor:   lfMinor,
		IsMain:    true,
	}
	pkgs := append([]*PackageMeta{main}, deps...)
	return &Info{
		Path:          path,
		Manifest:      &Manifest{SdkVersion: "test-sdk"},
		MainPackageID: mainPkgID,
		Packages:      pkgs,
	}
}

func pkg(name, version, id, lfMajor, lfMinor string) *PackageMeta {
	return &PackageMeta{
		PackageID: id,
		Name:      name,
		Version:   version,
		LFMajor:   lfMajor,
		LFMinor:   lfMinor,
	}
}

func TestCompare_UnchangedSamePkg(t *testing.T) {
	a := infoFixture("a.dar", "splice-wallet", "0.1.5", "id1", "2", "1",
		pkg("daml-prim", "", "p1", "2", "1"),
	)
	b := infoFixture("b.dar", "splice-wallet", "0.1.5", "id1", "2", "1",
		pkg("daml-prim", "", "p1", "2", "1"),
	)
	d := Compare(a, b)
	if !d.MainNameMatches {
		t.Error("names match should be true")
	}
	if d.MainVersionDelta != "unchanged" {
		t.Errorf("version delta = %q, want unchanged", d.MainVersionDelta)
	}
	if len(d.AddedPackages) != 0 || len(d.RemovedPackages) != 0 || len(d.ChangedPackages) != 0 {
		t.Error("expected no deltas")
	}
}

func TestCompare_VersionBumpedAndDepChange(t *testing.T) {
	a := infoFixture("a.dar", "splice-wallet", "0.1.5", "id1", "2", "1",
		pkg("splice-amulet", "0.1.5", "amu1", "2", "1"),
	)
	b := infoFixture("b.dar", "splice-wallet", "0.1.8", "id2", "2", "1",
		pkg("splice-amulet", "0.1.8", "amu2", "2", "1"),
	)
	d := Compare(a, b)
	if d.MainVersionDelta != "bumped" {
		t.Errorf("delta = %q", d.MainVersionDelta)
	}
	if len(d.ChangedPackages) != 1 {
		t.Fatalf("expected 1 changed dep, got %d", len(d.ChangedPackages))
	}
	if d.ChangedPackages[0].Name != "splice-amulet" {
		t.Errorf("changed dep name = %q", d.ChangedPackages[0].Name)
	}
	// SCU signals: should include INFO bump, INFO deps changed.
	foundBump := false
	for _, s := range d.SCUSignals {
		if s.Severity == "info" && strings.Contains(s.Message, "bumped") {
			foundBump = true
		}
	}
	if !foundBump {
		t.Error("missing info signal for bump")
	}
}

func TestCompare_Downgrade_Blocked(t *testing.T) {
	a := infoFixture("a.dar", "splice-wallet", "0.1.8", "id2", "2", "1")
	b := infoFixture("b.dar", "splice-wallet", "0.1.5", "id1", "2", "1")
	d := Compare(a, b)
	if d.MainVersionDelta != "downgraded" {
		t.Errorf("delta = %q", d.MainVersionDelta)
	}
	if !hasSignal(d, "block", "downgraded") {
		t.Error("expected BLOCK signal for downgrade")
	}
}

func TestCompare_DifferentNames_Blocked(t *testing.T) {
	a := infoFixture("a.dar", "splice-wallet", "0.1.5", "id1", "2", "1")
	b := infoFixture("b.dar", "splice-amulet", "0.1.5", "id1", "2", "1")
	d := Compare(a, b)
	if d.MainNameMatches {
		t.Error("names should NOT match")
	}
	if !hasSignal(d, "block", "names differ") {
		t.Error("expected BLOCK signal for name mismatch")
	}
}

func TestCompare_LFMajorChange_Blocked(t *testing.T) {
	a := infoFixture("a.dar", "splice-wallet", "0.1.5", "id1", "1", "14")
	b := infoFixture("b.dar", "splice-wallet", "0.1.8", "id2", "2", "1")
	d := Compare(a, b)
	if !d.MainLFMajorChanged {
		t.Error("LF major change should be detected")
	}
	if !hasSignal(d, "block", "Daml-LF major") {
		t.Error("expected BLOCK signal for LF major change")
	}
}

func TestCompareSemverish(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.5", "0.1.5", 0},
		{"0.1.5", "0.1.8", -1},
		{"0.1.8", "0.1.5", 1},
		{"1.0.0", "1.0.0-snapshot.123", 1},  // release > pre-release
		{"1.0.0-snapshot.123", "1.0.0", -1}, // pre-release < release (symmetric)
		{"0.1.10", "0.1.9", 1},              // numeric, not lexicographic
		// Two pre-releases: compared field-by-field, numeric < alphanumeric.
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, // numeric id ranks below alphanumeric
		// Real Splice snapshot promoted to its release tag.
		{"3.3.0-snapshot.20250502.13767.0.v2fc6c7e2", "3.3.0", -1},
		// Build metadata is ignored for precedence (semver §10).
		{"1.0.0+build1", "1.0.0+build2", 0},
	}
	for _, c := range cases {
		got := compareSemverish(c.a, c.b)
		// Normalize to -1/0/1 for the assert.
		switch {
		case got < 0:
			got = -1
		case got > 0:
			got = 1
		}
		if got != c.want {
			t.Errorf("compareSemverish(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionDelta(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"0.1.5", "0.1.5", "unchanged"},
		{"0.1.5", "0.1.8", "bumped"},
		{"0.1.8", "0.1.5", "downgraded"},
		{"", "0.1.0", "incomparable"},
		// Promoting a snapshot/pre-release build to its release version
		// is an UPGRADE, not a downgrade — the canonical Daml SCU flow.
		{"1.0.0-snapshot", "1.0.0", "bumped"},
		{"1.0.0", "1.0.0-snapshot", "downgraded"},
		{"3.3.0-snapshot.20250502.13767.0", "3.3.0", "bumped"},
	}
	for _, c := range cases {
		got := versionDelta(c.a, c.b)
		if got != c.want {
			t.Errorf("versionDelta(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// TestCompare_SnapshotToRelease_NotBlocked pins the end-to-end signal:
// promoting a pre-release main package to its release version must NOT
// surface a "block" SCU signal — flagging it as a downgrade would be
// the worst possible false positive for a triage tool.
func TestCompare_SnapshotToRelease_NotBlocked(t *testing.T) {
	a := infoFixture("a.dar", "splice-wallet", "1.0.0-snapshot.123", "id1", "2", "1")
	b := infoFixture("b.dar", "splice-wallet", "1.0.0", "id2", "2", "1")
	d := Compare(a, b)
	if d.MainVersionDelta != "bumped" {
		t.Errorf("delta = %q, want bumped", d.MainVersionDelta)
	}
	if hasSignal(d, "block", "downgraded") {
		t.Error("snapshot→release wrongly flagged as a BLOCK downgrade")
	}
	if !hasSignal(d, "info", "bumped") {
		t.Error("expected info signal for the version bump")
	}
}

func TestCompare_AdvisorySignalUsesOperatorFacingLanguage(t *testing.T) {
	a := infoFixture("a.dar", "splice-wallet", "0.1.5", "id1", "2", "1")
	b := infoFixture("b.dar", "splice-wallet", "0.1.5", "id1", "2", "1")
	d := Compare(a, b)
	if !hasSignal(d, "info", "Compatibility notes are advisory") {
		t.Fatal("missing advisory compatibility signal")
	}
	for _, s := range d.SCUSignals {
		if strings.Contains(s.Message, "best-effort signals") || strings.Contains(s.Message, "Ledger API") {
			t.Fatalf("SCU signal exposes implementation-heavy language: %q", s.Message)
		}
	}
}

func hasSignal(d *Diff, severity, substr string) bool {
	for _, s := range d.SCUSignals {
		if s.Severity == severity && strings.Contains(s.Message, substr) {
			return true
		}
	}
	return false
}
