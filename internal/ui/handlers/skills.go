// BIT-189 — Web UI Agent Skills screen backend.
//
// Serves the same embedded skill docs the CLI `localnet skills`
// command ships (internal/skills), so the two surfaces never drift.
// The browser can browse the docs and trigger a one-click install
// into the user's agent skills directory.
package handlers

import (
	"encoding/json"
	"net/http"

	api "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/skills"
)

// MountSkills installs the Agent Skills endpoints. Hub-independent
// (pure embedded-content + local filesystem writes).
//
//	GET  /api/skills              → list all skills (metadata + body)
//	POST /api/skills/install      → install into ~/.claude or ~/.codex
func MountSkills(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/skills", handleSkillsList)
	mux.HandleFunc("POST /api/skills/install", handleSkillsInstall)
}

func handleSkillsList(w http.ResponseWriter, _ *http.Request) {
	list, err := skills.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list skills", err)
		return
	}
	writeJSON(w, http.StatusOK, api.SkillsListResponse{
		SchemaVersion: api.SchemaVersion,
		Skills:        list,
	})
}

// skillsInstallRequest is the POST body. target is "claude" or
// "codex"; dir overrides the resolved path (rarely used from the UI);
// force overwrites locally-modified SKILL.md files.
type skillsInstallRequest struct {
	Target string `json:"target"`
	Dir    string `json:"dir,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

func handleSkillsInstall(w http.ResponseWriter, r *http.Request) {
	var req skillsInstallRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid request body",
			`expected {"target":"claude"|"codex","dir"?:"...","force"?:true}`)
		return
	}
	if req.Target == "" {
		req.Target = string(skills.TargetClaude)
	}
	dest, err := skills.TargetDir(skills.Target(req.Target), req.Dir)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid skills target",
			err.Error())
		return
	}
	res, err := skills.Install(dest, req.Force)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "install skills", err)
		return
	}
	writeJSON(w, http.StatusOK, api.SkillsInstallResponse{
		SchemaVersion: api.SchemaVersion,
		Target:        req.Target,
		Dir:           dest,
		Installed:     res.Written,
		Count:         len(res.Written),
		// skipped = locally-modified files preserved; re-POST with
		// {"force":true} to overwrite them.
		Skipped: res.Skipped,
	})
}
