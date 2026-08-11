package localnet

import "testing"

// TestExplorerCLIRoleFlags_DefaultAppProvider pins contracts/tx --role
// defaults to app-provider so they match the shared
// ResolveLedgerEndpoint empty-role fallback (and the Web Explorer).
func TestExplorerCLIRoleFlags_DefaultAppProvider(t *testing.T) {
	builders := []struct {
		name string
		flag func() string
	}{
		{"contracts ls", func() string { return buildContractsLs().Flags().Lookup("role").DefValue }},
		{"contracts watch", func() string { return buildContractsWatch().Flags().Lookup("role").DefValue }},
		{"tx ls", func() string { return buildTxLs().Flags().Lookup("role").DefValue }},
		{"tx replay", func() string { return buildTxReplay().Flags().Lookup("role").DefValue }},
	}
	for _, tc := range builders {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.flag(); got != "app-provider" {
				t.Errorf("--role default = %q, want app-provider", got)
			}
		})
	}
}
