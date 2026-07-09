package dar

import (
	"bytes"
	"strings"
	"testing"
)

// TestBuild_AllSubcommandsRegistered pins the CLI surface — adding
// or removing a `dar` subcommand requires updating this test, so a
// silent regression that drops, say, `upload` from the parent
// command tree is caught at build review time.
func TestBuild_AllSubcommandsRegistered(t *testing.T) {
	want := []string{
		"upload", "list", "download", "info", "diff",
		"remove", "build-upload", "watch",
	}
	root := Build(false)
	got := make(map[string]bool, len(root.Commands()))
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("dar subcommand %q missing from Build()", w)
		}
	}
	// Allow extras (a new subcommand is non-breaking), but flag it
	// if the count drops below the expected 8.
	if len(got) < len(want) {
		t.Errorf("got %d subcommands, want at least %d (%v)",
			len(got), len(want), want)
	}
}

// TestBuild_RootHelpMentionsKeyVerbs: smoke test that `dpm
// localnet dar --help` actually advertises the verbs we ship.
// Catches a regression where someone strips short descriptions
// from subcommand registration.
func TestBuild_RootHelpMentionsKeyVerbs(t *testing.T) {
	root := Build(false)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if err := root.Help(); err != nil {
		t.Fatalf("Help(): %v", err)
	}
	help := buf.String()
	for _, verb := range []string{"upload", "list", "info", "diff"} {
		if !strings.Contains(help, verb) {
			t.Errorf("help text missing verb %q\n%s", verb, help)
		}
	}
}

// TestUpload_RequiresArg: `dar upload` without a path argument
// must fail with a clear usage error, not segfault or hang.
func TestUpload_RequiresArg(t *testing.T) {
	root := Build(false)
	root.SetArgs([]string{"upload"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing DAR path arg")
	}
}

// TestDownload_RequiresArg: same for download — the package-id
// arg is required.
func TestDownload_RequiresArg(t *testing.T) {
	root := Build(false)
	root.SetArgs([]string{"download"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing package-id arg")
	}
}

// TestInfo_RequiresArg: info needs either a local DAR path or a
// package id.
func TestInfo_RequiresArg(t *testing.T) {
	root := Build(false)
	root.SetArgs([]string{"info"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing arg")
	}
}
