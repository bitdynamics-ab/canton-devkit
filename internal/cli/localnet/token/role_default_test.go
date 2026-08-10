package token

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	localtoken "github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// TestCLIRoleFlags_DefaultAppProvider pins every changed CLI --role
// flag default to token.DefaultRole (app-provider). Table-driven so a
// future verb that regresses to app-user fails loudly.
func TestCLIRoleFlags_DefaultAppProvider(t *testing.T) {
	// roleDefault returns the resolved --role default for a command,
	// looking through the parent's flag set when the flag is inherited
	// (e.g. the allocation-action subcommands register it on the parent).
	roleDefault := func(c *cobra.Command) string {
		if f := c.Flags().Lookup("role"); f != nil {
			return f.DefValue
		}
		if f := c.InheritedFlags().Lookup("role"); f != nil {
			return f.DefValue
		}
		return "<no --role flag>"
	}
	allocations := buildAllocations()
	sub := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		t.Fatalf("subcommand %q not found under %q", name, parent.Name())
		return nil
	}
	builders := []struct {
		name string
		flag func() string
	}{
		{"mint", func() string { return roleDefault(buildMint()) }},
		{"burn", func() string { return roleDefault(buildBurn()) }},
		{"transfer", func() string { return roleDefault(buildTransfer()) }},
		{"transfer accept", func() string { return roleDefault(buildTransferAccept()) }},
		{"activity", func() string { return roleDefault(buildActivity()) }},
		{"ls", func() string { return roleDefault(buildList()) }},
		{"allocate", func() string { return roleDefault(buildAllocate()) }},
		{"allocations", func() string { return roleDefault(allocations) }},
		{"allocations withdraw", func() string { return roleDefault(sub(allocations, "withdraw")) }},
		{"allocations cancel", func() string { return roleDefault(sub(allocations, "cancel")) }},
		{"balance", func() string { return roleDefault(buildBalance()) }},
		{"balances", func() string { return roleDefault(buildBalances()) }},
		{"create", func() string { return roleDefault(buildCreate()) }},
		{"demo", func() string { return roleDefault(buildDemo()) }},
		{"faucet", func() string { return roleDefault(buildFaucet()) }},
		{"party new", func() string { return roleDefault(buildPartyNew()) }},
		{"party ls", func() string { return roleDefault(buildPartyList()) }},
		{"summary", func() string { return roleDefault(buildSummary()) }},
		{"transfers", func() string { return roleDefault(buildTransfers()) }},
	}
	for _, tc := range builders {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.flag(); got != localtoken.DefaultRole {
				t.Errorf("--role default = %q, want %q", got, localtoken.DefaultRole)
			}
		})
	}
}

// TestMintBurnAccept_MissingPortSurfacesUnresolvedDiagnostic drives the
// real mint/burn/accept verbs with --instance only (no captured port)
// and asserts the shared missing-port remediation — not an opaque
// unsupported-instrument failure.
func TestMintBurnAccept_MissingPortSurfacesUnresolvedDiagnostic(t *testing.T) {
	verbs := []struct {
		name string
		run  func(out, errBuf *bytes.Buffer) error
	}{
		{
			name: "mint",
			run: func(out, errBuf *bytes.Buffer) error {
				c := buildMint()
				c.SetOut(out)
				c.SetErr(errBuf)
				c.SetArgs([]string{"--instance", "demo", "--instrument", "XYZ", "--to", "bob::xyz", "--amount", "1"})
				return c.Execute()
			},
		},
		{
			name: "burn",
			run: func(out, errBuf *bytes.Buffer) error {
				c := buildBurn()
				c.SetOut(out)
				c.SetErr(errBuf)
				c.SetArgs([]string{"--instance", "demo", "--instrument", "XYZ", "--from", "bob::xyz", "--amount", "1", "--yes"})
				return c.Execute()
			},
		},
		{
			name: "transfer accept",
			run: func(out, errBuf *bytes.Buffer) error {
				c := buildTransferAccept()
				c.SetOut(out)
				c.SetErr(errBuf)
				c.SetArgs([]string{"--instance", "demo", "--id", "cid-1"})
				return c.Execute()
			},
		},
	}
	for _, tc := range verbs {
		t.Run(tc.name, func(t *testing.T) {
			seedTokenInstance(t, "demo") // recorded token, no ledger port
			var out, errBuf bytes.Buffer
			_ = tc.run(&out, &errBuf)
			msg := errBuf.String()
			if !strings.Contains(msg, "no captured ledger port") {
				t.Errorf("want missing-port diagnostic, got: %q", msg)
			}
			if !strings.Contains(msg, "demo") || !strings.Contains(msg, localtoken.DefaultRole) {
				t.Errorf("diagnostic must name instance+role, got: %q", msg)
			}
			if strings.Contains(msg, "doesn't implement the V2 standard") {
				t.Errorf("must not mislabel missing port as unsupported instrument: %q", msg)
			}
		})
	}
}
