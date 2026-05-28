package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListReturnsAllSixSkills(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The proposal commits to six skill docs.
	want := map[string]bool{
		"localnet-lifecycle.md": false,
		"dar-upload.md":         false,
		"hot-deploy.md":         false,
		"inspect-contracts.md":  false,
		"token-flow.md":         false,
		"ci-localnet.md":        false,
	}
	for _, s := range list {
		if _, ok := want[s.Filename]; ok {
			want[s.Filename] = true
		}
		// Every doc must carry frontmatter name + description so both
		// the CLI list and the UI cards render meaningfully.
		if s.Name == "" {
			t.Errorf("%s has no name frontmatter", s.Filename)
		}
		if s.Description == "" {
			t.Errorf("%s has no description frontmatter", s.Filename)
		}
		if !strings.Contains(s.Body, "dpm localnet") &&
			!strings.Contains(s.Body, "canton-devkit localnet") {
			t.Errorf("%s body doesn't reference the CLI", s.Filename)
		}
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("expected skill doc %s missing", f)
		}
	}
	if len(list) != len(want) {
		t.Errorf("got %d skills, want %d", len(list), len(want))
	}
}

func TestListIsSortedByFilename(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Filename > list[i].Filename {
			t.Errorf("not sorted: %q before %q", list[i-1].Filename, list[i].Filename)
		}
	}
}

func TestInstallWritesOneSkillDirPerDoc(t *testing.T) {
	dir := t.TempDir()
	written, err := Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	list, _ := List()
	if len(written) != len(list) {
		t.Fatalf("wrote %d, expected %d", len(written), len(list))
	}
	for _, s := range list {
		dest := filepath.Join(dir, strings.TrimSuffix(s.Filename, ".md"), "SKILL.md")
		b, err := os.ReadFile(dest)
		if err != nil {
			t.Errorf("expected %s written: %v", dest, err)
			continue
		}
		if string(b) != s.Body {
			t.Errorf("%s content mismatch", dest)
		}
	}
}

func TestInstallOverwritesOnRerun(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Clobber one file, re-install, expect it restored.
	list, _ := List()
	victim := filepath.Join(dir, strings.TrimSuffix(list[0].Filename, ".md"), "SKILL.md")
	if err := os.WriteFile(victim, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatalf("second install: %v", err)
	}
	b, _ := os.ReadFile(victim)
	if string(b) == "tampered" {
		t.Error("re-install did not overwrite tampered file")
	}
}

func TestTargetDir(t *testing.T) {
	// Override wins.
	if got, _ := TargetDir(TargetClaude, "/tmp/x"); got != "/tmp/x" {
		t.Errorf("override ignored: %q", got)
	}
	// Known targets resolve under home.
	home, _ := os.UserHomeDir()
	if got, _ := TargetDir(TargetClaude, ""); got != filepath.Join(home, ".claude", "skills") {
		t.Errorf("claude dir = %q", got)
	}
	if got, _ := TargetDir(TargetCodex, ""); got != filepath.Join(home, ".codex", "skills") {
		t.Errorf("codex dir = %q", got)
	}
	// Unknown target errors.
	if _, err := TargetDir(Target("bogus"), ""); err == nil {
		t.Error("expected error for unknown target")
	}
}
