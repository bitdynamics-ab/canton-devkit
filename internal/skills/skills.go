// Package skills owns the build-time-embedded AI-agent skill
// documents shipped with canton-devkit (BIT-135 / BIT-189) and the
// logic to list, read, and install them.
//
// The skill docs are editor-agnostic markdown describing safe
// `dpm localnet` workflows. Both surfaces consume this package so
// they never drift (AGENTS.md CLI ↔ Web UI parity):
//   - the CLI `localnet skills list/install` command, and
//   - the Web UI Agent Skills screen's `/api/skills` handler.
//
// The docs live in ./docs/*.md, co-located with this file so the
// //go:embed directive stays inside the package directory (Go
// forbids `..` in embed patterns).
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed docs/*.md
var docsFS embed.FS

// Skill is one embedded skill document.
type Skill struct {
	// Filename is the doc's base name, e.g. "dar-upload.md".
	Filename string `json:"filename"`
	// Name is the `name:` frontmatter field (skill identifier).
	Name string `json:"name"`
	// Description is the `description:` frontmatter field.
	Description string `json:"description"`
	// Body is the full markdown (including frontmatter) — what gets
	// written on install and rendered in the UI.
	Body string `json:"body"`
}

// List returns every embedded skill, sorted by filename for stable
// output across CLI and UI.
func List() ([]Skill, error) {
	entries, err := fs.ReadDir(docsFS, "docs")
	if err != nil {
		return nil, fmt.Errorf("read embedded skills: %w", err)
	}
	out := make([]Skill, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := docsFS.ReadFile("docs/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		name, desc := parseFrontmatter(string(b))
		out = append(out, Skill{
			Filename:    e.Name(),
			Name:        name,
			Description: desc,
			Body:        string(b),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, nil
}

// parseFrontmatter extracts the `name:` and `description:` values from
// a leading `---`-delimited YAML frontmatter block. Returns empty
// strings when absent — the doc is still usable, just unlabeled.
func parseFrontmatter(body string) (name, description string) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, ln := range lines[1:] {
		t := strings.TrimSpace(ln)
		if t == "---" {
			break
		}
		if v, ok := strings.CutPrefix(t, "name:"); ok {
			name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(t, "description:"); ok {
			description = strings.TrimSpace(v)
		}
	}
	return name, description
}

// Target identifies a supported agent skill directory.
type Target string

const (
	TargetClaude Target = "claude"
	TargetCodex  Target = "codex"
)

// TargetDir resolves the on-disk skills directory for a target,
// honoring the agent's conventional location under the user's home:
//
//	claude → ~/.claude/skills
//	codex  → ~/.codex/skills
//
// A non-empty override wins (used by the CLI's --dir flag and tests).
func TargetDir(t Target, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	switch t {
	case TargetClaude:
		return filepath.Join(home, ".claude", "skills"), nil
	case TargetCodex:
		return filepath.Join(home, ".codex", "skills"), nil
	default:
		return "", fmt.Errorf("unknown skills target %q (use claude or codex)", t)
	}
}

// Install writes every embedded skill into dir (created if missing)
// and returns the absolute paths written. Each skill lands in its own
// subdirectory named after the doc (minus .md), matching the
// agent-skill convention of one directory per skill containing a
// SKILL.md. Overwrites existing files so re-running picks up updates.
func Install(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("install dir is empty")
	}
	all, err := List()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create skills dir: %w", err)
	}
	written := make([]string, 0, len(all))
	for _, s := range all {
		skillDir := filepath.Join(dir, strings.TrimSuffix(s.Filename, ".md"))
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return written, fmt.Errorf("create %s: %w", skillDir, err)
		}
		dest := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(dest, []byte(s.Body), 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", dest, err)
		}
		written = append(written, dest)
	}
	return written, nil
}
