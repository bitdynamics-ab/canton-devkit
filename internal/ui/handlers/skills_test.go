package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func skillsMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountSkills(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSkillsList_OK(t *testing.T) {
	srv := skillsMux(t)
	resp, err := http.Get(srv.URL + "/api/skills")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		SchemaVersion int `json:"schema_version"`
		Skills        []struct {
			Filename, Name, Description, Body string
		} `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", body.SchemaVersion)
	}
	if len(body.Skills) != 6 {
		t.Errorf("got %d skills, want 6", len(body.Skills))
	}
	for _, s := range body.Skills {
		if s.Name == "" || s.Body == "" {
			t.Errorf("skill %q missing name/body", s.Filename)
		}
	}
}

func TestSkillsInstall_ToTempDir(t *testing.T) {
	srv := skillsMux(t)
	// Install via explicit dir so we never touch the real ~/.claude.
	target := t.TempDir()
	reqBody, _ := json.Marshal(map[string]any{"target": "claude", "dir": target})
	resp, err := http.Post(srv.URL+"/api/skills/install", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Count     int      `json:"count"`
		Dir       string   `json:"dir"`
		Installed []string `json:"installed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 6 {
		t.Errorf("installed %d, want 6", body.Count)
	}
	if body.Dir != target {
		t.Errorf("dir = %q, want %q", body.Dir, target)
	}
	// At least one SKILL.md must exist on disk.
	if len(body.Installed) > 0 {
		if _, err := os.Stat(body.Installed[0]); err != nil {
			t.Errorf("reported install path not on disk: %v", err)
		}
	}
}

func TestSkillsInstall_BadTarget(t *testing.T) {
	srv := skillsMux(t)
	reqBody, _ := json.Marshal(map[string]any{"target": "bogus"})
	resp, err := http.Post(srv.URL+"/api/skills/install", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSkillsInstall_PreservesEditsWithoutForce(t *testing.T) {
	srv := skillsMux(t)
	target := t.TempDir()
	install := func(force bool) map[string]any {
		reqBody, _ := json.Marshal(map[string]any{"dir": target, "force": force})
		resp, err := http.Post(srv.URL+"/api/skills/install", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return body
	}
	first := install(false)
	// Tamper with one installed file.
	installed, _ := first["installed"].([]any)
	if len(installed) == 0 {
		t.Fatal("nothing installed")
	}
	victim := installed[0].(string)
	if err := os.WriteFile(victim, []byte("edited"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	// Re-install without force: edit preserved, victim reported skipped.
	second := install(false)
	if b, _ := os.ReadFile(victim); string(b) != "edited" {
		t.Error("edit clobbered without force")
	}
	skipped, _ := second["skipped"].([]any)
	if len(skipped) == 0 {
		t.Error("expected skipped to report the edited file")
	}
	_ = filepath.Base(victim)
}
