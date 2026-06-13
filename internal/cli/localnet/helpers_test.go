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
