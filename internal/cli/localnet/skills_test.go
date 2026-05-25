package localnet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillsList_NamesAllBundled is the pin that catches "a
// contributor added a new skill .md but the embed didn't pick it
// up." If a new file lands in skills_embed/ but go:embed wasn't
// rebuilt, this test still works (embed is rebuilt on every `go
// test`); the failure mode would be the OPPOSITE — removing a file
// from disk that the test still expects.
//
// We assert that EVERY canonical skill ships. The list mirrors
// skills_embed/README.md's "What ships" table — keep them in
// sync if a skill is added or removed.
func TestSkillsList_NamesAllBundled(t *testing.T) {
	cmd := buildSkills()
	cmd.SetArgs([]string{"list"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v\n%s", err, out.String())
	}

	body := out.String()
	for _, want := range []string{
		"localnet-lifecycle.md",
		"dar-upload.md",
		"hot-deploy.md",
		"inspect-contracts.md",
		"token-flow.md",
		"ci-localnet.md",
		"README.md",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skills list missing %q\nfull:\n%s", want, body)
		}
	}
}

// TestSkillsInstall_CopiesAllFiles is the integration smoke for
// `skills install`: install into a tempdir, verify each .md file
// landed with non-empty contents. Catches regressions in the
// embed-FS read path AND the destination MkdirAll/WriteFile path.
func TestSkillsInstall_CopiesAllFiles(t *testing.T) {
	dest := t.TempDir()
	cmd := buildSkills()
	cmd.SetArgs([]string{"install", "--target", dest})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}

	// Every expected file must exist and be non-empty.
	for _, name := range []string{
		"localnet-lifecycle.md", "dar-upload.md", "hot-deploy.md",
		"inspect-contracts.md", "token-flow.md", "ci-localnet.md",
		"README.md",
	} {
		path := filepath.Join(dest, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing installed file %q: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("installed file %q is empty", name)
		}
	}
}

// TestSkillsInstall_DryRunWritesNothing pins the contract: --dry-run
// must not create the target directory and must not write any
// files. Catches the regression class where the dry-run flag is
// honored for the announce but not for the write.
func TestSkillsInstall_DryRunWritesNothing(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "does-not-exist-yet")

	cmd := buildSkills()
	cmd.SetArgs([]string{"install", "--target", dest, "--dry-run"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run install: %v\n%s", err, out.String())
	}

	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("dry-run output should announce itself, got %q", out.String())
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dry-run created target dir %q — must be a no-op on disk", dest)
	}
}

// TestSkillsInstall_OverwritesExistingFile verifies the
// version-locked / idempotent install: re-installing over an
// existing file replaces its content (so `dpm upgrade` followed
// by re-install picks up new skill text).
func TestSkillsInstall_OverwritesExistingFile(t *testing.T) {
	dest := t.TempDir()
	stale := filepath.Join(dest, "localnet-lifecycle.md")
	if err := os.WriteFile(stale, []byte("OLD STALE CONTENT"), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	cmd := buildSkills()
	cmd.SetArgs([]string{"install", "--target", dest})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	body, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "OLD STALE") {
		t.Error("install did not overwrite the stale file — re-install isn't idempotent")
	}
	// Sanity: the new content has the YAML frontmatter we ship.
	if !strings.Contains(string(body), "name: canton-devkit-lifecycle") {
		t.Errorf("overwritten file doesn't look like the bundled skill: %q",
			body[:min(120, len(body))])
	}
}

// TestResolveSkillsTarget_DefaultsToClaude pins the default-target
// rule: empty --target → ~/.claude/skills/canton-devkit/. Matches the
// "Install" button text in the JSX Agent screen mockup. Changing
// this would silently move where the docs land for every existing
// user; the test makes that change visible at review time.
func TestResolveSkillsTarget_DefaultsToClaude(t *testing.T) {
	got, err := resolveSkillsTarget("")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(".claude", "skills", "canton-devkit")
	if !strings.HasSuffix(got, want) {
		t.Errorf("default target = %q, want suffix %q", got, want)
	}
}

// TestResolveSkillsTarget_ExpandsTilde verifies ~ and ~/path are
// expanded to the user's home dir. A `--target ~/...` call from the
// shell would already be expanded by the shell, but `--target=~/...`
// (with the = form) passes the literal tilde through cobra, and the
// resolver has to do it.
func TestResolveSkillsTarget_ExpandsTilde(t *testing.T) {
	got, err := resolveSkillsTarget("~/.codex/skills/canton-devkit")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.HasPrefix(got, "~") {
		t.Errorf("tilde not expanded: %q", got)
	}
	if !strings.Contains(got, ".codex/skills/canton-devkit") {
		t.Errorf("tilde expansion lost the suffix: %q", got)
	}
}
