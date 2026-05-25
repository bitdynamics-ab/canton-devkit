package localnet

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// skillsFS embeds the bundled agent skill .md files.
//
// The canonical files live at internal/cli/localnet/skills_embed/
// rather than skills/ at the repo root because Go's //go:embed
// directive disallows parent-directory traversal (`..` is rejected).
// The CLI is the only Go consumer; the repo-root skills/README.md
// points contributors to the embed location.
//
// New .md files dropped into the embed directory ship automatically
// — the embed pattern is data-driven, no per-file list to maintain.
//
//go:embed all:skills_embed
var skillsFS embed.FS

// skillsEmbedRoot is the subdirectory inside skillsFS that holds the
// actual .md files. Kept as a constant so a future reorg (e.g. nesting
// by category) is a one-line change.
const skillsEmbedRoot = "skills_embed"

// buildSkills wires `dpm localnet skills` and its `install`
// subcommand. The verb separation leaves room for future siblings
// (`skills list`, `skills uninstall`) without changing the existing
// CLI surface.
func buildSkills() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage DevKit AI-agent skill docs",
		Long: `Skill docs teach an AI coding agent (Claude Code, Codex, Cursor)
how to use ` + "`dpm localnet`" + ` safely — boot a ledger, upload a DAR,
inspect contracts, run a CIP-0112 token flow — without giving the
agent raw shell access. Each .md file in this command's bundle is a
self-contained skill the agent loads from its skills folder.

The 2026-05-25 Web UI Agent screen renders the same files. See
skills/README.md for authoring rules.`,
	}
	cmd.AddCommand(buildSkillsInstall())
	cmd.AddCommand(buildSkillsList())
	cmd.AddCommand(buildSkillsUninstall())
	return cmd
}

// buildSkillsUninstall implements `dpm localnet skills uninstall`.
//
// Reviewer pin (PR #44 round-2 uninstall): symmetric to install.
// Removes ONLY the .md files this binary knows about — never the
// target directory itself, never files we didn't ship — so a
// user who hand-edited additional files in the same dir doesn't
// lose them. Idempotent (missing files are fine).
//
// Future "skill upgrade" workflow: `uninstall && install` flushes
// a stale doc set when the .md content changes between dpm
// versions.
func buildSkillsUninstall() *cobra.Command {
	var (
		target string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the bundled skill docs from the target dir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dest, err := resolveSkillsTarget(target)
			if err != nil {
				return err
			}
			files, err := listSkillFiles()
			if err != nil {
				return err
			}
			absDest, err := filepath.Abs(dest)
			if err != nil {
				return fmt.Errorf("resolve target: %w", err)
			}

			removed := 0
			for _, f := range files {
				outPath := filepath.Join(dest, f)
				// Same zip-slip guard as install — never remove
				// anything outside the resolved target.
				if err := assertInsideDir(absDest, outPath); err != nil {
					return fmt.Errorf("refusing to remove %s: %w", f, err)
				}
				if dryRun {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc("  would remove "+outPath))
					removed++
					continue
				}
				err := os.Remove(outPath)
				if err == nil {
					removed++
					_, _ = fmt.Fprintln(cmd.OutOrStdout(),
						term.Step(term.StepCheck, "removed", f, ""))
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("remove %s: %w", outPath, err)
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc(
				fmt.Sprintf("uninstalled %d skill(s) from %s", removed, dest)))
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "",
		"Source directory to clean. Default: ~/.claude/skills/canton-devkit/")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Print what would be removed without touching the filesystem.")
	return cmd
}

// buildSkillsInstall implements `dpm localnet skills install`.
//
// Default target = ~/.claude/skills/canton-devkit/ (matches the
// AgentScreen mockup's "Install" button text). --target overrides
// for codex/cursor/other layouts.
//
// Existing target files are overwritten — the install is idempotent
// and version-locked to the running `dpm` binary. Pass --dry-run to
// see what would be copied without touching the filesystem.
func buildSkillsInstall() *cobra.Command {
	var (
		target string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Copy skill docs into your agent's skills folder",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dest, err := resolveSkillsTarget(target)
			if err != nil {
				return err
			}

			files, err := listSkillFiles()
			if err != nil {
				return err
			}
			if len(files) == 0 {
				return fmt.Errorf("no embedded skill files found — was the build run without populating internal/cli/localnet/%s?", skillsEmbedRoot)
			}

			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc(
					fmt.Sprintf("dry-run: would copy %d file(s) to %s",
						len(files), dest)))
				for _, f := range files {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc("  "+f))
				}
				return nil
			}

			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("create target %s: %w", dest, err)
			}

			// Reviewer pin (PR #44 round-2 zip-slip): validate the
			// resolved outPath stays under dest BEFORE writing.
			// The current source (embed.FS) is trusted, but
			// defence-in-depth means a future change that lets
			// users contribute skill files can't sneak in
			// `../../etc/passwd.md` and have it overwrite
			// arbitrary host files. The validation is one
			// filepath.Rel + a prefix check — cheap and exact.
			absDest, err := filepath.Abs(dest)
			if err != nil {
				return fmt.Errorf("resolve target dir: %w", err)
			}
			for _, f := range files {
				body, err := fs.ReadFile(skillsFS,
					filepath.ToSlash(filepath.Join(skillsEmbedRoot, f)))
				if err != nil {
					return fmt.Errorf("read embedded %s: %w", f, err)
				}
				outPath := filepath.Join(dest, f)
				if err := assertInsideDir(absDest, outPath); err != nil {
					return fmt.Errorf("refusing to write %s: %w", f, err)
				}
				if err := os.WriteFile(outPath, body, 0o644); err != nil {
					return fmt.Errorf("write %s: %w", outPath, err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Step(term.StepCheck,
					f, dest, ""))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), term.Dimc(
				fmt.Sprintf("installed %d skill(s) to %s", len(files), dest)))
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "",
		"Destination directory. Default: ~/.claude/skills/canton-devkit/")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Print what would be copied without touching the filesystem.")
	return cmd
}

// buildSkillsList implements `dpm localnet skills list` — prints the
// names of bundled skills so a script can iterate.
func buildSkillsList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the skill docs bundled with this dpm binary",
		RunE: func(cmd *cobra.Command, _ []string) error {
			files, err := listSkillFiles()
			if err != nil {
				return err
			}
			for _, f := range files {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), f)
			}
			return nil
		},
	}
}

// assertInsideDir is the zip-slip guard used by install. Returns
// an error if `path` resolves to anything outside `absDir`.
// filepath.Rel + a "is the result relative and non-traversal"
// check is the textbook safe-extraction pattern.
//
// Reviewer pin (PR #44 round-2 zip-slip): even though the
// current source (embed.FS) is trusted, future code that lets
// users contribute skill files must not be able to overwrite
// arbitrary host files via "../../etc/passwd.md".
func assertInsideDir(absDir, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absDir, abs)
	if err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return fmt.Errorf("path resolves to the target dir itself, not a file inside it")
	}
	if strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)+"..") {
		return fmt.Errorf("path %q escapes target dir %q (rel=%q)", path, absDir, rel)
	}
	return nil
}

// resolveSkillsTarget expands ~ and applies the default if empty.
// Returning the resolved absolute path keeps the caller's error
// messages friendly (no "~/foo" tail-blindness).
func resolveSkillsTarget(raw string) (string, error) {
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, ".claude", "skills", "canton-devkit"), nil
	}
	if strings.HasPrefix(raw, "~/") || raw == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(raw, "~/")), nil
	}
	return raw, nil
}

// listSkillFiles enumerates the .md files in the embedded skills
// directory. README.md is included intentionally — it's the index
// the agent uses to know which skills are available.
func listSkillFiles() ([]string, error) {
	entries, err := fs.ReadDir(skillsFS, skillsEmbedRoot)
	if err != nil {
		return nil, fmt.Errorf("read embedded skills: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}
