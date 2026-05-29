package types

import "github.com/bitdynamics-ab/canton-devkit/internal/skills"

// SkillsListResponse is the shared CLI --json and Web UI response for
// the bundled agent skill catalogue.
type SkillsListResponse struct {
	SchemaVersion int            `json:"schema_version"`
	Skills        []skills.Skill `json:"skills"`
}

// SkillsInstallResponse is the shared response for installing bundled
// agent skills into a target directory.
type SkillsInstallResponse struct {
	SchemaVersion int      `json:"schema_version"`
	Target        string   `json:"target"`
	Dir           string   `json:"dir"`
	Installed     []string `json:"installed"`
	Count         int      `json:"count"`
	Skipped       []string `json:"skipped"`
}
