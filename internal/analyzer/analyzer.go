// Package analyzer wraps the vendored Certora daml-analyzer JAR: it locates
// a Java runtime and the jar, runs the analyzer on a compiled .dar, and maps
// its JSON output onto internal/api/types. Both the CLI (`dpm localnet dar
// analyze`) and the Web UI analyzer handlers call through here, so the two
// surfaces produce identical reports.
package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// ErrJavaNotFound / ErrJarNotFound are sentinels so callers can render a
// clean "not configured" state with remediation instead of a raw failure.
var (
	ErrJavaNotFound = errors.New("java runtime not found")
	ErrJarNotFound  = errors.New("daml-analyzer jar not found")
)

// JarEnv overrides the vendored jar location.
const JarEnv = "DAML_ANALYZER_JAR"

// vendoredJarRelPaths are tried relative to the executable and the working
// directory when JarEnv is unset (the jar is vendored under third_party/).
var vendoredJarRelPaths = []string{
	"third_party/daml-analyzer/daml-analyzer.jar",
	"../third_party/daml-analyzer/daml-analyzer.jar",
	"../../third_party/daml-analyzer/daml-analyzer.jar",
}

// FindJava returns the java executable (PATH first, then JAVA_HOME/bin/java).
func FindJava() (string, error) {
	if p, err := exec.LookPath("java"); err == nil {
		return p, nil
	}
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		if cand := filepath.Join(jh, "bin", "java"); fileExists(cand) {
			return cand, nil
		}
	}
	return "", ErrJavaNotFound
}

// FindJar locates the vendored analyzer jar (JarEnv wins, else search).
func FindJar() (string, error) {
	if p := os.Getenv(JarEnv); p != "" {
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("%w: %s=%q does not exist", ErrJarNotFound, JarEnv, p)
	}
	var roots []string
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd)
	}
	for _, root := range roots {
		for _, rel := range vendoredJarRelPaths {
			if cand := filepath.Join(root, rel); fileExists(cand) {
				return cand, nil
			}
		}
	}
	return "", ErrJarNotFound
}

// Status probes whether an analysis can run in this environment, so the UI
// shows a "not configured" state up front rather than failing mid-analysis.
func Status(ctx context.Context) types.AnalyzerStatusResponse {
	st := types.AnalyzerStatusResponse{SchemaVersion: types.SchemaVersion}
	if java, err := FindJava(); err == nil {
		st.JavaFound = true
		st.JavaVersion = javaVersion(ctx, java)
	}
	if _, err := FindJar(); err == nil {
		st.JarFound = true
	}
	st.Available = st.JavaFound && st.JarFound
	switch {
	case !st.JavaFound:
		st.Detail = "install a Java runtime (JRE 17+) or set JAVA_HOME"
	case !st.JarFound:
		st.Detail = "analyzer jar not found; set " + JarEnv + " or vendor third_party/daml-analyzer/daml-analyzer.jar"
	}
	return st
}

func javaVersion(ctx context.Context, java string) string {
	out, err := exec.CommandContext(ctx, java, "-version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
}

// AnalyzeBytes writes dar bytes to a temp file and analyzes it. Used by the
// Web UI, which fetches the .dar payload from the participant over gRPC.
func AnalyzeBytes(ctx context.Context, dar []byte) (*types.AnalyzerReport, error) {
	dir, err := os.MkdirTemp("", "dpm-analyze-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	darPath := filepath.Join(dir, "package.dar")
	if err := os.WriteFile(darPath, dar, 0o600); err != nil {
		return nil, fmt.Errorf("write dar: %w", err)
	}
	return AnalyzeDAR(ctx, darPath)
}

// AnalyzeDAR runs the analyzer on a .dar path and returns the parsed report.
func AnalyzeDAR(ctx context.Context, darPath string) (*types.AnalyzerReport, error) {
	java, err := FindJava()
	if err != nil {
		return nil, err
	}
	jar, err := FindJar()
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	// -Xss4m: the analyzer recurses over deep package graphs and upstream
	// recommends a larger stack. -f json streams the report to stdout.
	cmd := exec.CommandContext(ctx, java, "-Xss4m", "-jar", jar, darPath, "-f", "json")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("daml-analyzer failed: %s", msg)
	}
	return parseReport(stdout.Bytes())
}

func parseReport(raw []byte) (*types.AnalyzerReport, error) {
	var r rawReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse analyzer output: %w", err)
	}
	return r.toTypes(), nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// --- upstream JSON shape (camelCase) → devkit types (snake_case) ----------

type rawReport struct {
	AnalyzedPackage rawPkg           `json:"analyzedPackage"`
	Dependencies    []rawPkgRef      `json:"dependencies"`
	Summary         rawSummary       `json:"summary"`
	Interactions    []rawInteraction `json:"interactions"`
}

type rawPkg struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	PackageID string `json:"packageId"`
	LfVersion string `json:"lfVersion"`
}

type rawPkgRef struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	PackageID string `json:"packageId"`
}

type rawSummary struct {
	TotalInteractions int            `json:"totalInteractions"`
	ByType            map[string]int `json:"byType"`
	ByTargetPackage   map[string]int `json:"byTargetPackage"`
}

type rawSource struct {
	Package   string `json:"package"`
	File      string `json:"file"`
	StartLine *int   `json:"startLine"`
}

type rawEndpoint struct {
	Package   string `json:"package"`
	Version   string `json:"version"`
	PackageID string `json:"packageId"`
	Module    string `json:"module"`
	Template  string `json:"template"`
	Interface string `json:"interface"`
	Choice    string `json:"choice"`
	Consuming *bool  `json:"consuming"`
}

type rawInteraction struct {
	Type   string      `json:"type"`
	Source *rawSource  `json:"source"`
	Caller rawEndpoint `json:"caller"`
	Target rawEndpoint `json:"target"`
}

func (r rawReport) toTypes() *types.AnalyzerReport {
	out := &types.AnalyzerReport{
		AnalyzedPackage: types.AnalyzerPackage{
			Name: r.AnalyzedPackage.Name, Version: r.AnalyzedPackage.Version,
			PackageID: r.AnalyzedPackage.PackageID, LFVersion: r.AnalyzedPackage.LfVersion,
		},
		Summary: types.AnalyzerSummary{
			TotalInteractions: r.Summary.TotalInteractions,
			ByType:            r.Summary.ByType,
			ByTargetPackage:   r.Summary.ByTargetPackage,
		},
	}
	for _, d := range r.Dependencies {
		out.Dependencies = append(out.Dependencies, types.AnalyzerPackageRef{
			Name: d.Name, Version: d.Version, PackageID: d.PackageID,
		})
	}
	for _, it := range r.Interactions {
		conv := types.AnalyzerInteraction{
			Type:   it.Type,
			Caller: mapEndpoint(it.Caller),
			Target: mapEndpoint(it.Target),
		}
		if it.Source != nil {
			conv.Source = &types.AnalyzerSource{
				Package: it.Source.Package, File: it.Source.File, StartLine: it.Source.StartLine,
			}
		}
		out.Interactions = append(out.Interactions, conv)
	}
	return out
}

func mapEndpoint(e rawEndpoint) types.AnalyzerEndpoint {
	return types.AnalyzerEndpoint{
		Package: e.Package, Version: e.Version, PackageID: e.PackageID, Module: e.Module,
		Template: e.Template, Interface: e.Interface, Choice: e.Choice, Consuming: e.Consuming,
	}
}
