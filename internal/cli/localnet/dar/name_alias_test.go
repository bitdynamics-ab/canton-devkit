package dar

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestConnectFlags_NameAliasesInstance verifies the dar verbs accept
// --name as an alias for --instance, so the same instance selector works
// across the whole CLI. Without the normalize alias a user who typed
// `dar list --name foo` hit "unknown flag: --name".
func TestConnectFlags_NameAliasesInstance(t *testing.T) {
	var f connectFlags
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	f.register(cmd)
	cmd.SetArgs([]string{"--name", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute with --name: %v", err)
	}
	if f.Instance != "demo" {
		t.Errorf("--name did not alias to --instance; f.Instance = %q, want %q", f.Instance, "demo")
	}

	// --instance still works unchanged.
	var g connectFlags
	cmd2 := &cobra.Command{Use: "y", RunE: func(*cobra.Command, []string) error { return nil }}
	g.register(cmd2)
	cmd2.SetArgs([]string{"--instance", "bar"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("execute with --instance: %v", err)
	}
	if g.Instance != "bar" {
		t.Errorf("--instance broken by the alias; g.Instance = %q, want %q", g.Instance, "bar")
	}
}
