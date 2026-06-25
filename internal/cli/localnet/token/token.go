// Package token wires the `dpm localnet token` cobra subtree. Mirrors
// the dar subtree's layout (one file per verb, shared parent in
// token.go).
package token

import (
	"github.com/spf13/cobra"
)

// Build returns the `token` parent command. Adds subcommands inline
// so the call site (internal/cli/localnet/localnet.go) just does
// `AddCommand(token.Build())`.
func Build() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Create and manage Canton Token Standard instruments on LocalNet",
		Long: `Manage Canton Token Standard instruments on a LocalNet instance.

Subcommands cover create, mint, transfer, burn, balances, and party
aliases. Works with V1/CIP-0056 (e.g. Amulet on Splice 0.6.4) and
V2/CIP-0112 (HoldingV2; needs --profile tokens-v2 at up time).

Quick start:
  token create  --instance <i> --endpoint <p> --non-interactive --name … --symbol …
  token mint    --instance <i> --endpoint <p> --instrument <sym> --to <party> --amount …
  token balances --instance <i> --endpoint <p>
  token party new <alias> --instance <i> --endpoint <p>`,
	}
	cmd.AddCommand(buildCreate())
	cmd.AddCommand(buildDemo())
	cmd.AddCommand(buildMint())
	cmd.AddCommand(buildTransfer())
	cmd.AddCommand(buildBurn())
	cmd.AddCommand(buildBalance())
	cmd.AddCommand(buildBalances())
	cmd.AddCommand(buildSummary())
	cmd.AddCommand(buildActivity())
	cmd.AddCommand(buildParty())
	cmd.AddCommand(buildFaucet())
	return cmd
}

// errSilent is returned by mint/transfer/burn when the orchestration
// surfaced ErrNeedsV2LocalNet — cobra exits non-zero, but the user
// already saw the friendly remediation on stderr so we suppress the
// usual usage-and-stack dump. Implements `error` with an empty string
// so cobra's default error renderer is a no-op.
var errSilent silentError

type silentError struct{}

func (silentError) Error() string { return "" }
