package token

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestTokenBundleCommit_FromCuratedCatalogue resolves a curated tag to
// its pinned commit (the V2 alpha entry ships in versions.json).
func TestTokenBundleCommit_FromCuratedCatalogue(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "token-standard-v2")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	commit, err := tokenBundleCommit("demo")
	if err != nil {
		t.Fatalf("resolve commit: %v", err)
	}
	if len(commit) < 12 {
		t.Errorf("commit looks wrong: %q", commit)
	}
}

func TestTokenBundleCommit_UnknownVersionErrors(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "does-not-exist-9.9.9")
	s.ProjectDir = t.TempDir()
	s.DataDir = t.TempDir()
	if err := registry.Write(s); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenBundleCommit("demo"); err == nil {
		t.Error("want error for an unknown Splice version")
	}
}
