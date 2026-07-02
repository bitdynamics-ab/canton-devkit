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
	// The bundled catalogue ships exactly these six skill docs.
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
	res, err := Install(dir, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	list, _ := List()
	if len(res.Written) != len(list) {
		t.Fatalf("wrote %d, expected %d", len(res.Written), len(list))
	}
	if len(res.Skipped) != 0 {
		t.Errorf("fresh install should skip nothing, got %v", res.Skipped)
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

func TestInstallReinstallIdenticalIsNoConflict(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Re-install over identical content: everything counts as written
	// (idempotent), nothing skipped — picking up DevKit updates stays
	// frictionless.
	res, err := Install(dir, false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("identical re-install should skip nothing, got %v", res.Skipped)
	}
}

func TestInstallPreservesLocalEditsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	list, _ := List()
	victim := filepath.Join(dir, strings.TrimSuffix(list[0].Filename, ".md"), "SKILL.md")
	if err := os.WriteFile(victim, []byte("my local edit"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}

	// Without --force: the edited file must be PRESERVED + reported skipped.
	res, err := Install(dir, false)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "my local edit" {
		t.Error("local edit was clobbered without --force")
	}
	found := false
	for _, p := range res.Skipped {
		if p == victim {
			found = true
		}
	}
	if !found {
		t.Errorf("edited file not reported in Skipped: %v", res.Skipped)
	}

	// With force=true: the edit is overwritten back to the bundled body.
	res2, err := Install(dir, true)
	if err != nil {
		t.Fatalf("force reinstall: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) == "my local edit" {
		t.Error("--force did not overwrite the local edit")
	}
	if len(res2.Skipped) != 0 {
		t.Errorf("force install should skip nothing, got %v", res2.Skipped)
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
