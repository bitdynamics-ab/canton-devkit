package localnet

import (
	"bytes"
	"io"
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

// TestSkillsInstall_AssertInsideDir is the reviewer pin (PR #44
// round-2 zip-slip): the install path-traversal guard must reject
// any computed outPath that escapes the resolved target dir. We
// exercise assertInsideDir directly with adversarial inputs;
// the production caller uses it from inside the install loop.
func TestSkillsInstall_AssertInsideDir(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		path    string
		wantErr bool
	}{
		{filepath.Join(dir, "skill.md"), false},
		{filepath.Join(dir, "sub", "skill.md"), false},
		{filepath.Join(dir, "..", "escape.md"), true},
		{filepath.Join(dir, "..", "..", "etc", "passwd"), true},
		{"/etc/passwd", true},
		{dir, true}, // path is the dir itself, not a file inside
	}
	for _, c := range cases {
		err := assertInsideDir(dir, c.path)
		if (err != nil) != c.wantErr {
			t.Errorf("assertInsideDir(%q, %q) err=%v, wantErr=%v",
				dir, c.path, err, c.wantErr)
		}
	}
}

// TestSkillsUninstall_RemovesInstalledFiles pins the symmetric
// path: install + uninstall returns the dir to its prior state
// (modulo any hand-written files outside our bundle).
func TestSkillsUninstall_RemovesInstalledFiles(t *testing.T) {
	dest := t.TempDir()

	// Install.
	install := buildSkills()
	install.SetArgs([]string{"install", "--target", dest})
	var out bytes.Buffer
	install.SetOut(&out)
	install.SetErr(&out)
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}

	// Plant a hand-written file the uninstall MUST NOT touch.
	keepPath := filepath.Join(dest, "my-private-notes.md")
	if err := os.WriteFile(keepPath, []byte("user-owned"), 0o644); err != nil {
		t.Fatalf("seed user file: %v", err)
	}

	// Uninstall.
	uninstall := buildSkills()
	uninstall.SetArgs([]string{"uninstall", "--target", dest})
	out.Reset()
	uninstall.SetOut(&out)
	uninstall.SetErr(&out)
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}

	// Every bundled skill file gone; user file preserved.
	bundled, _ := listSkillFiles()
	for _, f := range bundled {
		if _, err := os.Stat(filepath.Join(dest, f)); !os.IsNotExist(err) {
			t.Errorf("bundled %s still present after uninstall (err=%v)", f, err)
		}
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Errorf("user file removed by uninstall: %v", err)
	}
}

// TestSkillsUninstall_DryRunRemovesNothing — --dry-run on
// uninstall mirrors install: prints what would happen, touches
// nothing.
func TestSkillsUninstall_DryRunRemovesNothing(t *testing.T) {
	dest := t.TempDir()

	install := buildSkills()
	install.SetArgs([]string{"install", "--target", dest})
	install.SetOut(io.Discard)
	install.SetErr(io.Discard)
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := buildSkills()
	uninstall.SetArgs([]string{"uninstall", "--target", dest, "--dry-run"})
	var out bytes.Buffer
	uninstall.SetOut(&out)
	uninstall.SetErr(&out)
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("dry-run uninstall: %v", err)
	}
	if !strings.Contains(out.String(), "would remove") {
		t.Errorf("dry-run output should say 'would remove', got %q", out.String())
	}
	// Files still present.
	bundled, _ := listSkillFiles()
	for _, f := range bundled {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("dry-run uninstall removed file %s: %v", f, err)
		}
	}
}

// TestSkillsFutureVerbAgentGuidance is the reviewer pin (PR #44
// round-2 future-verb gating): every skill whose YAML frontmatter
// declares `status: planned` MUST also carry an AI-agent guidance
// block telling the agent to run `dpm localnet --help` first and
// refuse if the verb isn't available. Without this, the agent
// downloads the doc, follows the recipe, and runs commands that
// don't exist.
//
// Detection: scan for `status: planned` in the embedded files;
// for each match, assert "For AI agents" appears in the body.
func TestSkillsFutureVerbAgentGuidance(t *testing.T) {
	entries, _ := skillsFS.ReadDir(skillsEmbedRoot)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := skillsFS.ReadFile(filepath.ToSlash(filepath.Join(skillsEmbedRoot, e.Name())))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if !strings.Contains(string(body), "status: planned") {
			continue
		}
		checked++
		if !strings.Contains(string(body), "For AI agents") {
			t.Errorf("%s declares status: planned but has no 'For AI agents' guidance block — agent will run vapor commands",
				e.Name())
		}
		if !strings.Contains(string(body), "dpm localnet --help") {
			t.Errorf("%s declares status: planned but doesn't tell agent to verify via `dpm localnet --help`",
				e.Name())
		}
	}
	if checked == 0 {
		t.Skip("no status:planned skills in this branch — lint not exercised")
	}
}

// TestSkillsAllHaveSPDXHeader is the reviewer pin (PR #44 round-2
// SPDX): every .md file in the embedded skills set must carry an
// SPDX-License-Identifier marker so downstream consumers
// (homebrew formula auditors, package signers, license scrapers)
// can attribute the file correctly.
func TestSkillsAllHaveSPDXHeader(t *testing.T) {
	entries, _ := skillsFS.ReadDir(skillsEmbedRoot)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, _ := skillsFS.ReadFile(filepath.ToSlash(filepath.Join(skillsEmbedRoot, e.Name())))
		if !strings.Contains(string(body), "SPDX-License-Identifier:") {
			t.Errorf("%s missing SPDX-License-Identifier", e.Name())
		}
	}
}
