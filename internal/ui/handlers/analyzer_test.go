package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/analyzer"
)

// pkgAppDAR is the sample DAR the analyzer tests run against. Shared
// on disk with the analyzer's own test fixtures; DAML_ANALYZER_IMAGE
// must point at a built analyzer image for these to pass.
const pkgAppDAR = "testdata/pkg-app.dar"

// analyzerMux mounts the analyzer endpoints on an isolated test surface.
func analyzerMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountAnalyzer(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestAnalyzerStatus asserts the status endpoint reports the schema
// version and a reachable Docker (DAML_ANALYZER_IMAGE is set for the
// suite, and Docker is running).
func TestAnalyzerStatus(t *testing.T) {
	if !analyzer.Status(context.Background()).Available {
		t.Skip("no analyzer runtime available (DPM component or Docker)")
	}
	srv := analyzerMux(t)

	resp, err := http.Get(srv.URL + "/api/analyzer/status")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		SchemaVersion int    `json:"schema_version"`
		Available     bool   `json:"available"`
		Runtime       string `json:"runtime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SchemaVersion == 0 {
		t.Errorf("schema_version not set: %+v", got)
	}
	if !got.Available || got.Runtime == "" {
		t.Errorf("available/runtime = %v/%q, want true + a resolved runtime", got.Available, got.Runtime)
	}
}

// TestAnalyzerAnalyzeUpload drives the upload endpoint end-to-end: POST
// the pkg-app DAR bytes as multipart field `dar`, expect a 200 with a
// report naming the analyzed package and a non-zero interaction count.
func TestAnalyzerAnalyzeUpload(t *testing.T) {
	if !analyzer.Status(context.Background()).Available {
		t.Skip("no analyzer runtime available (DPM component or Docker)")
	}
	darBytes, err := os.ReadFile(pkgAppDAR)
	if err != nil {
		t.Skipf("sample DAR unavailable: %v", err)
	}
	srv := analyzerMux(t)

	body, ct := analyzerUploadBody(t, "pkg-app-1.0.0.dar", darBytes)
	resp, err := http.Post(srv.URL+"/api/analyzer/analyze", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, raw)
	}

	var got struct {
		SchemaVersion int    `json:"schema_version"`
		DarName       string `json:"dar_name"`
		Report        struct {
			AnalyzedPackage struct {
				Name string `json:"name"`
			} `json:"analyzed_package"`
			Summary struct {
				TotalInteractions int `json:"total_interactions"`
			} `json:"summary"`
		} `json:"report"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DarName != "pkg-app-1.0.0.dar" {
		t.Errorf("dar_name = %q, want pkg-app-1.0.0.dar", got.DarName)
	}
	if got.Report.AnalyzedPackage.Name != "pkg-app" {
		t.Errorf("analyzed_package.name = %q, want pkg-app", got.Report.AnalyzedPackage.Name)
	}
	if got.Report.Summary.TotalInteractions <= 0 {
		t.Errorf("total_interactions = %d, want > 0", got.Report.Summary.TotalInteractions)
	}
}

// analyzerUploadBody builds a multipart body carrying one `dar` file
// part with the given bytes. Returns the body and its Content-Type.
func analyzerUploadBody(t *testing.T, filename string, fileBytes []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("dar", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(fileBytes); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
