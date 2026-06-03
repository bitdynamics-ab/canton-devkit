package localnet

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the localnet skills subtree with args and returns
// stdout, stderr, and the cobra error.
func runSkills(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := buildSkills()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errb.String(), err
}

func TestSkillsList_Text(t *testing.T) {
	out, errb, err := runSkills(t, "list")
	if err != nil {
		t.Fatalf("list: %v (stderr=%q)", err, errb)
	}
	for _, want := range []string{"localnet-lifecycle.md", "dar-upload.md", "token-flow.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\n%s", want, out)
		}
	}
}

func TestSkillsList_JSON(t *testing.T) {
	out, _, err := runSkills(t, "list", "--format", "json")
	if err != nil {
		t.Fatalf("list json: %v", err)
	}
	var body struct {
		SchemaVersion int `json:"schema_version"`
		Skills        []struct {
			Filename string `json:"filename"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out)
	}
	if body.SchemaVersion != 1 || len(body.Skills) != 6 {
		t.Errorf("schema=%d skills=%d, want 1 and 6", body.SchemaVersion, len(body.Skills))
	}
}

func TestSkillsList_BadFormat(t *testing.T) {
	_, _, err := runSkills(t, "list", "--format", "yaml")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestSkillsInstall_ExplicitDir(t *testing.T) {
	dir := t.TempDir()
	out, errb, err := runSkills(t, "install", "--dir", dir)
	if err != nil {
		t.Fatalf("install: %v (stderr=%q)", err, errb)
	}
	if !strings.Contains(out, "Installed 6 skill(s)") {
		t.Errorf("unexpected install output:\n%s", out)
	}
	// Spot-check one file landed.
	if _, statErr := os.Stat(filepath.Join(dir, "dar-upload", "SKILL.md")); statErr != nil {
		t.Errorf("expected dar-upload/SKILL.md: %v", statErr)
	}
}

func TestSkillsInstall_DefaultTargetUsesHome(t *testing.T) {
	// Inject a fake HOME so we never touch the real ~/.claude.
	home := t.TempDir()
	t.Setenv("HOME", home)                  // unix
	t.Setenv("USERPROFILE", home)           // windows (os.UserHomeDir)
	_, errb, err := runSkills(t, "install") // default --target claude
	if err != nil {
		t.Fatalf("install: %v (stderr=%q)", err, errb)
	}
	want := filepath.Join(home, ".claude", "skills", "ci-localnet", "SKILL.md")
	if _, statErr := os.Stat(want); statErr != nil {
		t.Errorf("expected %s: %v", want, statErr)
	}
}

func TestSkillsInstall_BadTarget(t *testing.T) {
	_, _, err := runSkills(t, "install", "--target", "bogus")
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
}
