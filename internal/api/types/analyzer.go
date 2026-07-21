package types

// Wire types for the daml-analyzer integration: a static analysis of the
// cross-package interactions in one compiled Daml package (.dar). These
// mirror the upstream analyzer's JSON (github.com/Certora/daml-analyzer),
// re-tagged to the devkit's snake_case convention; the internal/analyzer
// package maps the analyzer's camelCase output onto them.

// AnalyzerPackage is the analyzed package's identity.
type AnalyzerPackage struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	PackageID string `json:"package_id"`
	LFVersion string `json:"lf_version,omitempty"`
}

// AnalyzerPackageRef names a dependency package.
type AnalyzerPackageRef struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	PackageID string `json:"package_id"`
}

// AnalyzerSummary aggregates the interactions by type and by target package.
type AnalyzerSummary struct {
	TotalInteractions int            `json:"total_interactions"`
	ByType            map[string]int `json:"by_type"`
	ByTargetPackage   map[string]int `json:"by_target_package"`
}

// AnalyzerSource is the (best-effort) source location of an interaction.
type AnalyzerSource struct {
	Package   string `json:"package,omitempty"`
	File      string `json:"file,omitempty"`
	StartLine *int   `json:"start_line,omitempty"`
}

// AnalyzerEndpoint is one side of an interaction (caller or target): the
// package + module and, where applicable, the template / interface /
// choice. Consuming is set on choice targets.
type AnalyzerEndpoint struct {
	Package   string `json:"package"`
	Version   string `json:"version"`
	PackageID string `json:"package_id"`
	Module    string `json:"module"`
	Template  string `json:"template,omitempty"`
	Interface string `json:"interface,omitempty"`
	Choice    string `json:"choice,omitempty"`
	Consuming *bool  `json:"consuming,omitempty"`
}

// AnalyzerInteraction is a single cross-package interaction: a caller in
// the analyzed package reaching a target in a dependency. Type is one of
// Create / Exercise / Fetch / FetchInterface / ImplementsInterface / etc.
type AnalyzerInteraction struct {
	Type   string           `json:"type"`
	Source *AnalyzerSource  `json:"source,omitempty"`
	Caller AnalyzerEndpoint `json:"caller"`
	Target AnalyzerEndpoint `json:"target"`
}

// AnalyzerReport is the full analysis of one package.
type AnalyzerReport struct {
	AnalyzedPackage AnalyzerPackage       `json:"analyzed_package"`
	Dependencies    []AnalyzerPackageRef  `json:"dependencies"`
	Summary         AnalyzerSummary       `json:"summary"`
	Interactions    []AnalyzerInteraction `json:"interactions"`
}

// AnalyzerResponse is the top-level shape for GET /api/instances/{name}/analyzer/{id}
// and the CLI `dpm localnet dar analyze --json`.
type AnalyzerResponse struct {
	SchemaVersion int             `json:"schema_version"`
	Instance      string          `json:"instance,omitempty"`
	DarName       string          `json:"dar_name,omitempty"`
	PackageID     string          `json:"package_id,omitempty"`
	Report        *AnalyzerReport `json:"report"`
}

// AnalyzerStatusResponse reports whether the analyzer can run in this
// environment (Docker reachable, image present/pullable), so the UI can
// show a clean "not configured" state instead of failing an analysis
// mid-flight.
type AnalyzerStatusResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Available     bool   `json:"available"`
	DockerFound   bool   `json:"docker_found"`
	ImagePresent  bool   `json:"image_present"`
	Image         string `json:"image,omitempty"`
	Detail        string `json:"detail,omitempty"`
}
