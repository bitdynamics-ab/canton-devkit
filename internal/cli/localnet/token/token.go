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
		Short: "Create and manage Canton Token Standard V2 instruments on LocalNet",
		Long: `Manage Token Standard V2 token instruments on a LocalNet instance.

The subcommands wrap the upstream V2 flow ([HoldingV2], TransferInstructionV2,
BurnMintV1) for the convenience of a local developer: create a fresh
issuer-managed instrument, mint/transfer/burn holdings, query balances.

V2 only — V1 / CIP-0056 is not supported by this CLI. Selecting an
alpha-channel Splice version with the --profile tokens-v2 overlay is
required for the on-ledger surfaces to function (see "localnet versions"
+ "localnet up --profile tokens-v2").

[HoldingV2]: https://github.com/canton-network/splice/blob/token-standard-v2-upcoming/token-standard/splice-api-token-holding-v2/daml/Splice/Api/Token/HoldingV2.daml`,
	}
	cmd.AddCommand(buildCreate())
	cmd.AddCommand(buildMint())
	cmd.AddCommand(buildTransfer())
	cmd.AddCommand(buildBurn())
	cmd.AddCommand(buildBalance())
	cmd.AddCommand(buildBalances())
	cmd.AddCommand(buildSummary())
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
