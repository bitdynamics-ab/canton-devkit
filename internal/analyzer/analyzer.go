// Package analyzer runs the Certora daml-analyzer as a pinned Docker image:
// it checks Docker, runs the image on a compiled .dar (mounted read-only),
// and maps the analyzer's JSON output onto internal/api/types. Both the CLI
// (`dpm localnet dar analyze`) and the Web UI analyzer handlers call through
// here, so the two surfaces produce identical reports. Running the analyzer
// as a container keeps it reproducible (pinned image) with no host Java and
// nothing heavy in git — the image lives in a registry.
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

// ErrDockerNotFound is a sentinel so callers can render a clean "not
// configured" state with remediation instead of a raw failure.
var ErrDockerNotFound = errors.New("docker not found")

const (
	// ImageEnv overrides the analyzer image reference (for a locally-built
	// image, or a mirror). Defaults to DefaultImage.
	ImageEnv = "DAML_ANALYZER_IMAGE"
	// DefaultImage is the pinned, published analyzer image. It is built from
	// the upstream commit recorded in build/daml-analyzer/Dockerfile.
	DefaultImage = "ghcr.io/bitdynamics-ab/daml-analyzer:0.1.0-143a7e2"
)

// Image returns the analyzer image reference (env override wins).
func Image() string {
	if e := strings.TrimSpace(os.Getenv(ImageEnv)); e != "" {
		return e
	}
	return DefaultImage
}

// FindDocker returns the docker executable path.
func FindDocker() (string, error) {
	if p, err := exec.LookPath("docker"); err == nil {
		return p, nil
	}
	return "", ErrDockerNotFound
}

func imagePresent(ctx context.Context, docker, image string) bool {
	return exec.CommandContext(ctx, docker, "image", "inspect", image).Run() == nil
}

// Status probes whether an analysis can run here (docker installed, daemon
// reachable, image present or pullable), so the UI shows a "not configured"
// state up front rather than failing mid-analysis.
func Status(ctx context.Context) types.AnalyzerStatusResponse {
	st := types.AnalyzerStatusResponse{SchemaVersion: types.SchemaVersion, Image: Image()}
	docker, err := FindDocker()
	if err != nil {
		st.Detail = "install Docker to run the analyzer image"
		return st
	}
	st.DockerFound = true
	if exec.CommandContext(ctx, docker, "info").Run() != nil {
		st.Detail = "Docker is installed but the daemon is not reachable"
		return st
	}
	st.ImagePresent = imagePresent(ctx, docker, st.Image)
	// Available once the daemon is reachable; a missing image is pulled on
	// first analysis.
	st.Available = true
	if !st.ImagePresent {
		st.Detail = "image will be pulled on first analysis: " + st.Image
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

// AnalyzeDAR runs the analyzer image on a .dar path and returns the parsed
// report. The dar's directory is mounted read-only (mounting the directory
// rather than the single file is the portable choice across Docker backends).
func AnalyzeDAR(ctx context.Context, darPath string) (*types.AnalyzerReport, error) {
	docker, err := FindDocker()
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(darPath)
	if err != nil {
		return nil, fmt.Errorf("resolve dar path: %w", err)
	}
	image := Image()
	if !imagePresent(ctx, docker, image) {
		// Pull on demand; the run below surfaces a clear error if the image
		// genuinely isn't available (offline / not yet published).
		_ = exec.CommandContext(ctx, docker, "pull", image).Run()
	}

	var stdout, stderr bytes.Buffer
	// ENTRYPOINT is `java -jar <analyzer>`, so the args are the in-container
	// dar path + output format. --network none: analysis needs no network.
	cmd := exec.CommandContext(ctx, docker, "run", "--rm", "--network", "none",
		"-v", filepath.Dir(abs)+":/in:ro",
		image, "/in/"+filepath.Base(abs), "-f", "json")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("daml-analyzer (docker) failed: %s", msg)
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
