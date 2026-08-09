package token

import (
	"bytes"
	"strings"
	"testing"

	localtoken "github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// TestCLIRoleFlags_DefaultAppProvider pins every changed CLI --role
// flag default to token.DefaultRole (app-provider). Table-driven so a
// future verb that regresses to app-user fails loudly.
func TestCLIRoleFlags_DefaultAppProvider(t *testing.T) {
	builders := []struct {
		name string
		flag func() string
	}{
		{"mint", func() string { return buildMint().Flags().Lookup("role").DefValue }},
		{"burn", func() string { return buildBurn().Flags().Lookup("role").DefValue }},
		{"transfer", func() string { return buildTransfer().Flags().Lookup("role").DefValue }},
		{"transfer accept", func() string { return buildTransferAccept().Flags().Lookup("role").DefValue }},
		{"activity", func() string { return buildActivity().Flags().Lookup("role").DefValue }},
		{"ls", func() string { return buildList().Flags().Lookup("role").DefValue }},
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
