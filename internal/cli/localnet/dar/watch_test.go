package dar

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFile writes content under root, creating parent dirs, and
// stamps a deterministic mtime so hashSources' (size, mtime) tuple is
// stable across the test regardless of filesystem timestamp
// granularity.
func writeFile(t *testing.T, root, rel, content string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", rel, err)
	}
	return p
}

// TestHashSources_DetectsDamlYamlChange is the regression test for the
// hot-deploy loop that never rebuilt on a version bump. daml.yaml is
// the file edited to bump the package `version` (the canonical SCU
// loop); a change to it MUST change the source hash so the watch loop
// triggers a rebuild.
func TestHashSources_DetectsDamlYamlChange(t *testing.T) {
	root := t.TempDir()
	base := time.Unix(1_700_000_000, 0)
	writeFile(t, root, "daml.yaml", "version: 0.1.0\nname: demo\n", base)
	writeFile(t, root, "daml/Main.daml", "module Main where\n", base)

	before, err := hashSources(root)
	if err != nil {
		t.Fatalf("hashSources (before): %v", err)
	}

	// Bump the version — same edit a developer makes for an SCU
	// upgrade. Different length + later mtime so the change is
	// unambiguous.
	writeFile(t, root, "daml.yaml", "version: 0.2.0\nname: demo\n", base.Add(time.Minute))

	after, err := hashSources(root)
	if err != nil {
		t.Fatalf("hashSources (after): %v", err)
	}
	if before == after {
		t.Error("editing daml.yaml did not change the source hash — watch would never rebuild on a version bump")
	}
}

// TestHashSources_StillDetectsDamlChange is the symmetric pin: .daml
// edits must also change the source hash.
func TestHashSources_StillDetectsDamlChange(t *testing.T) {
	root := t.TempDir()
	base := time.Unix(1_700_000_000, 0)
	writeFile(t, root, "daml.yaml", "version: 0.1.0\n", base)
	writeFile(t, root, "daml/Main.daml", "module Main where\n", base)

	before, err := hashSources(root)
	if err != nil {
		t.Fatalf("hashSources (before): %v", err)
	}
	writeFile(t, root, "daml/Main.daml", "module Main where\n-- edit\n", base.Add(time.Minute))
	after, err := hashSources(root)
	if err != nil {
		t.Fatalf("hashSources (after): %v", err)
	}
	if before == after {
		t.Error("editing a .daml file did not change the source hash")
	}
}

// TestHashSources_IgnoresGeneratedDir pins that files under the
// generated .daml/ build output are excluded — otherwise a fresh build
// (which writes .daml/dist/*.dar and a generated .daml/...yaml) would
// immediately re-trigger the loop. We drop a .yaml inside .daml/ and
// assert the hash is unchanged.
func TestHashSources_IgnoresGeneratedDir(t *testing.T) {
	root := t.TempDir()
	base := time.Unix(1_700_000_000, 0)
	writeFile(t, root, "daml.yaml", "version: 0.1.0\n", base)
	writeFile(t, root, "daml/Main.daml", "module Main where\n", base)

	before, err := hashSources(root)
	if err != nil {
		t.Fatalf("hashSources (before): %v", err)
	}
	// Build output: a yaml and a daml under .daml/ must NOT count.
	writeFile(t, root, ".daml/package-config.yaml", "generated: true\n", base.Add(time.Minute))
	writeFile(t, root, ".daml/dist/generated.daml", "module Gen where\n", base.Add(time.Minute))
	after, err := hashSources(root)
	if err != nil {
		t.Fatalf("hashSources (after): %v", err)
	}
	if before != after {
		t.Error("files under .daml/ leaked into the source hash — a build would self-retrigger")
	}
}

// TestIsWatchedSource pins the extension set so a future edit can't
// silently drop yaml or daml.
func TestIsWatchedSource(t *testing.T) {
	cases := map[string]bool{
		"Main.daml":     true,
		"daml.yaml":     true,
		"deps.yml":      true,
		"README.md":     false,
		"foo.dar":       false,
		"Makefile":      false,
		"daml.yaml.bak": false,
		"notes.txt":     false,
	}
	for name, want := range cases {
		if got := isWatchedSource(name); got != want {
			t.Errorf("isWatchedSource(%q) = %v, want %v", name, got, want)
		}
	}
}
