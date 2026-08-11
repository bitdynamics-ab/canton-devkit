package dar

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestDARConnectRole_DefaultAppProvider pins the shared --role flag
// default used by every dar-admin verb (list/upload/inspect/…).
func TestDARConnectRole_DefaultAppProvider(t *testing.T) {
	var f connectFlags
	cmd := &cobra.Command{Use: "test"}
	f.register(cmd)
	got := cmd.Flags().Lookup("role").DefValue
	if got != "app-provider" {
		t.Errorf("--role default = %q, want app-provider", got)
	}
}
