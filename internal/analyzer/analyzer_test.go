package analyzer

import (
	"os"
	"testing"
)

// TestParseReport pins the camelCase-upstream → snake_case-devkit mapping
// against a real analyzer output fixture (pkg-app, which reaches two
// dependency packages), so a wire-shape change upstream is caught here.
func TestParseReport(t *testing.T) {
	raw, err := os.ReadFile("testdata/pkg-app.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	r, err := parseReport(raw)
	if err != nil {
		t.Fatalf("parseReport: %v", err)
	}

	if r.AnalyzedPackage.Name != "pkg-app" || r.AnalyzedPackage.LFVersion == "" {
		t.Errorf("analyzed package: got %+v", r.AnalyzedPackage)
	}
	if r.Summary.TotalInteractions != len(r.Interactions) {
		t.Errorf("summary total %d != %d interactions", r.Summary.TotalInteractions, len(r.Interactions))
	}
	if r.Summary.ByType["Exercise"] == 0 && r.Summary.ByType["Fetch"] == 0 {
		t.Errorf("byType not populated: %+v", r.Summary.ByType)
	}
	if len(r.Dependencies) == 0 || r.Dependencies[0].PackageID == "" {
		t.Errorf("dependencies not mapped: %+v", r.Dependencies)
	}

	// At least one interaction must carry a mapped target (proves the
	// camelCase "package"/"packageId" fields landed on our snake_case ones)
	// and a source file (proves nested optional mapping).
	var sawTarget, sawSource bool
	for _, it := range r.Interactions {
		if it.Type == "" || it.Target.Package == "" || it.Caller.Package == "" {
			t.Errorf("interaction missing core fields: %+v", it)
		}
		if it.Target.PackageID != "" && it.Target.Module != "" {
			sawTarget = true
		}
		if it.Source != nil && it.Source.File != "" {
			sawSource = true
		}
	}
	if !sawTarget {
		t.Error("no interaction had a fully-mapped target")
	}
	if !sawSource {
		t.Error("no interaction had a mapped source location")
	}
}

func TestImage_EnvOverride(t *testing.T) {
	t.Setenv(ImageEnv, "example.com/mirror/daml-analyzer:pinned")
	if got := Image(); got != "example.com/mirror/daml-analyzer:pinned" {
		t.Errorf("Image() env override: got %q", got)
	}
	t.Setenv(ImageEnv, "")
	if got := Image(); got != DefaultImage {
		t.Errorf("Image() default: got %q, want %q", got, DefaultImage)
	}
}
