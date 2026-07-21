package dar

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/analyzer"
	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// TestDarAnalyzeJSON runs the analyze verb on a committed sample DAR and
// asserts the --json output. Requires a built analyzer image + Docker, so
// it skips when DAML_ANALYZER_IMAGE is unset.
func TestDarAnalyzeJSON(t *testing.T) {
	if os.Getenv(analyzer.ImageEnv) == "" {
		t.Skipf("%s not set; build the analyzer image to run this", analyzer.ImageEnv)
	}
	cmd := buildAnalyze()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"testdata/pkg-app.dar", "--format", "json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v; out=%s", err, out.String())
	}

	var resp types.AnalyzerResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode json: %v; out=%s", err, out.String())
	}
	if resp.SchemaVersion != types.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", resp.SchemaVersion, types.SchemaVersion)
	}
	if resp.Report == nil || resp.Report.AnalyzedPackage.Name != "pkg-app" {
		t.Fatalf("unexpected report: %+v", resp.Report)
	}
	if resp.Report.Summary.TotalInteractions == 0 {
		t.Error("expected non-zero interactions")
	}
}

// TestDarAnalyzeBadFormat rejects an unknown --format without touching Docker.
func TestDarAnalyzeBadFormat(t *testing.T) {
	cmd := buildAnalyze()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"testdata/pkg-app.dar", "--format", "yaml"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Error("expected an error for --format yaml")
	}
}
