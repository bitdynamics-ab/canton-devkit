package localnet

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newTestCmd creates a minimal cobra.Command with a --name string
// flag for testing resolveName.
func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "test [name]",
	}
	cmd.Flags().String("name", "", "Instance name.")
	return cmd
}

func TestResolveName_PositionalOnly(t *testing.T) {
	cmd := newTestCmd()
	got, err := resolveName(cmd, []string{"mynet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mynet" {
		t.Errorf("got %q, want %q", got, "mynet")
	}
}

func TestResolveName_FlagOnly(t *testing.T) {
	cmd := newTestCmd()
	if err := cmd.Flags().Set("name", "mynet"); err != nil {
		t.Fatal(err)
	}
	got, err := resolveName(cmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mynet" {
		t.Errorf("got %q, want %q", got, "mynet")
	}
}

func TestResolveName_BothErrors(t *testing.T) {
	cmd := newTestCmd()
	if err := cmd.Flags().Set("name", "flagname"); err != nil {
		t.Fatal(err)
	}
	_, err := resolveName(cmd, []string{"posname"})
	if err == nil {
		t.Fatal("expected error when both positional and --name are provided")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Errorf("error = %q, want substring %q", err, "not both")
	}
}

func TestResolveName_NeitherErrors(t *testing.T) {
	cmd := newTestCmd()
	_, err := resolveName(cmd, nil)
	if err == nil {
		t.Fatal("expected error when neither positional nor --name is provided")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want substring %q", err, "required")
	}
}

func TestResolveName_EmptyPositionalTreatedAsAbsent(t *testing.T) {
	// `up ""` must not count as a provided name: it falls through to the
	// generic "name required" message, not a blank-name validation error.
	cmd := newTestCmd()
	if _, err := resolveName(cmd, []string{""}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("empty positional: err = %v, want the generic name-required message", err)
	}
	if _, err := resolveName(newTestCmd(), []string{"   "}); err == nil {
		t.Error("whitespace-only positional should be treated as absent")
	}

	// An empty positional alongside --name is NOT ambiguous; --name wins.
	cmd2 := newTestCmd()
	if err := cmd2.Flags().Set("name", "dev"); err != nil {
		t.Fatal(err)
	}
	got, err := resolveName(cmd2, []string{""})
	if err != nil {
		t.Fatalf("empty positional + --name should resolve to the flag, got %v", err)
	}
	if got != "dev" {
		t.Errorf("resolved = %q, want dev", got)
	}
}
