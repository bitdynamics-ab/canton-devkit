package token

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestBuildBalances_EndpointOptional pins the consistency fix: `token
// balances` resolves the participant ledger endpoint from --instance
// (like `token balance` and `contracts`), so --endpoint is optional, not
// required. --instance stays required.
func TestBuildBalances_EndpointOptional(t *testing.T) {
	cmd := buildBalances()

	ep := cmd.Flags().Lookup("endpoint")
	if ep == nil {
		t.Fatal("balances has no --endpoint flag")
	}
	if _, required := ep.Annotations[cobra.BashCompOneRequiredFlag]; required {
		t.Error("--endpoint must NOT be required — it resolves from --instance")
	}

	inst := cmd.Flags().Lookup("instance")
	if inst == nil {
		t.Fatal("balances has no --instance flag")
	}
	if _, required := inst.Annotations[cobra.BashCompOneRequiredFlag]; !required {
		t.Error("--instance must stay required")
	}
}
