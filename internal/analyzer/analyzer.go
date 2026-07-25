// Package analyzer runs Certora's daml-analyzer over a compiled .dar and maps
// its JSON onto internal/api/types. Both the CLI (`dpm localnet dar analyze`)
// and the Web UI analyzer handlers call through here, so the two surfaces
// produce identical reports.
//
// Runtime resolution, in priority order:
//
//  1. DAML_ANALYZER_BIN — an explicit executable taking `<dar> -f json`.
//  2. The DPM component (oci://ghcr.io/certora/daml-analyzer), installed with
//     `dpm install package` and cached under the DPM root. This is the
//     supported path: upstream-published and versioned by DPM. The devkit
//     execs the component's wrapper directly, so it works from any directory
//     (the `dpm certora-analyze` subcommand only resolves inside a project).
//     The wrapper runs a bundled jar, so it needs a JVM on PATH.
//  3. The pinned Docker image — needs no host Java, used when no component is
//     installed.
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
	"sort"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// ErrNoRuntime is a sentinel so callers can render a clean "not configured"
// state with remediation instead of a raw failure.
var ErrNoRuntime = errors.New("no daml-analyzer runtime available")

const (
	// BinEnv points at an analyzer executable directly (component wrapper,
	// or any script accepting `<dar> -f json`). Wins over every other path.
	BinEnv = "DAML_ANALYZER_BIN"
	// ImageEnv overrides the Docker image reference (local build or mirror).
	ImageEnv = "DAML_ANALYZER_IMAGE"
	// DefaultImage is the fallback image, built from the pinned upstream
	// commit in build/daml-analyzer/Dockerfile.
	DefaultImage = "ghcr.io/bitdynamics-ab/daml-analyzer:0.1.0-143a7e2"

	// componentPath is the DPM component's cache location relative to the DPM
	// root, and the wrapper the component publishes.
	componentPath    = "cache/components/ghcr.io/certora/daml-analyzer"
	componentWrapper = "bin/daml-analyzer.sh"
	// ComponentRef is what a user adds to daml.yaml to install the component.
	ComponentRef = "oci://ghcr.io/certora/daml-analyzer:0.1.0"
)

// Runtime is how the analyzer will be invoked.
type Runtime struct {
	Kind    string // "bin" | "component" | "docker"
	Path    string // executable path, or image reference for docker
	Version string // component version, when known
}

// Image returns the Docker image reference (env override wins).
func Image() string {
	if e := strings.TrimSpace(os.Getenv(ImageEnv)); e != "" {
		return e
	}
	return DefaultImage
}

// dpmRoot is where DPM caches components ($DPM_HOME, else ~/.dpm).
func dpmRoot() string {
	if h := strings.TrimSpace(os.Getenv("DPM_HOME")); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dpm")
}

// FindComponent returns the DPM-installed analyzer wrapper and its version,
// picking the highest version when several are cached. ok=false when the
// component isn't installed.
func FindComponent() (path, version string, ok bool) {
	root := dpmRoot()
	if root == "" {
		return "", "", false
	}
	base := filepath.Join(root, componentPath)
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", "", false
	}
	versions := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions))) // highest first
	for _, v := range versions {
		if cand := filepath.Join(base, v, componentWrapper); fileExists(cand) {
			return cand, v, true
		}
	}
	return "", "", false
}

// FindDocker returns the docker executable path.
func FindDocker() (string, error) {
	return exec.LookPath("docker")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dockerUsable(ctx context.Context) (string, bool) {
	docker, err := FindDocker()
	if err != nil {
		return "", false
	}
	return docker, exec.CommandContext(ctx, docker, "info").Run() == nil
}

// ResolveRuntime picks how to run the analyzer, honouring the priority order
// documented on the package.
func ResolveRuntime(ctx context.Context) (Runtime, error) {
	if b := strings.TrimSpace(os.Getenv(BinEnv)); b != "" {
		if !fileExists(b) {
			return Runtime{}, fmt.Errorf("%w: %s=%q does not exist", ErrNoRuntime, BinEnv, b)
		}
		return Runtime{Kind: "bin", Path: b}, nil
	}
	if p, v, ok := FindComponent(); ok {
		return Runtime{Kind: "component", Path: p, Version: v}, nil
	}
	if docker, ok := dockerUsable(ctx); ok {
		return Runtime{Kind: "docker", Path: docker, Version: Image()}, nil
	}
	return Runtime{}, ErrNoRuntime
}

// Status probes whether an analysis can run here, so the UI shows a "not
// configured" state up front rather than failing mid-analysis.
func Status(ctx context.Context) types.AnalyzerStatusResponse {
	st := types.AnalyzerStatusResponse{SchemaVersion: types.SchemaVersion}
	rt, err := ResolveRuntime(ctx)
	if err != nil {
		st.Detail = "install the analyzer as a DPM component (add `" + ComponentRef +
			"` to daml.yaml, then `dpm install package`), or install Docker"
		return st
	}
	st.Available = true
	st.Runtime = rt.Kind
	switch rt.Kind {
	case "component":
		st.Source = "dpm component " + rt.Version
		if _, jerr := exec.LookPath("java"); jerr != nil {
			// The component wraps a jar, so a missing JVM only shows up at
			// run time — surface it now.
			st.Available = false
			st.Detail = "DPM component found but no Java runtime on PATH (the component runs a bundled jar)"
		}
	case "docker":
		st.Source = rt.Version
	default:
		st.Source = rt.Path
	}
	return st
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
	abs, err := filepath.Abs(darPath)
	if err != nil {
		return nil, fmt.Errorf("resolve dar path: %w", err)
	}
	rt, err := ResolveRuntime(ctx)
	if err != nil {
		return nil, err
	}

	var cmd *exec.Cmd
	switch rt.Kind {
	case "docker":
		// The dar's directory is mounted read-only (mounting the directory
		// rather than the file is portable across Docker backends); the image
		// entrypoint is the analyzer. Analysis needs no network.
		cmd = exec.CommandContext(ctx, rt.Path, "run", "--rm", "--network", "none",
			"-v", filepath.Dir(abs)+":/in:ro",
			Image(), "/in/"+filepath.Base(abs), "-f", "json")
	default: // "component" / "bin" — the wrapper takes the dar path directly
		cmd = exec.CommandContext(ctx, rt.Path, abs, "-f", "json")
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("daml-analyzer (%s) failed: %s", rt.Kind, msg)
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
