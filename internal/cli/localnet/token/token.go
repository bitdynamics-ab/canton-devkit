// Package token wires the `dpm localnet token` cobra subtree, one file
// per verb.
package token

import (
	"github.com/spf13/cobra"
)

// Build returns the assembled `token` parent command.
func Build() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Create and manage Canton Token Standard instruments on LocalNet",
		Long: `Manage Canton Token Standard token instruments on a LocalNet instance.

The subcommands wrap the upstream token-standard flow ([HoldingV2] /
TransferInstructionV2 and their V1/CIP-0056 counterparts) for the
convenience of a local developer: create a fresh issuer-managed
instrument, mint/transfer/burn holdings, fund parties, and query balances —
all by party alias, with no JWTs, ports, or contract ids in your face.

Both token-standard generations are supported and routed per instrument:
V1 / CIP-0056 (what real assets like Amulet / Canton Coin implement on
stable releases such as Splice 0.6.4) and V2 / CIP-0112 (the alpha
HoldingV2 / TransferInstructionV2 surface). Read + transfer/faucet work
against either. Creating a NEW on-ledger instrument uses the bundled
splice-test-token-v2, which needs an alpha-channel Splice version with the
--profile tokens-v2 overlay (see "localnet versions" + "localnet up
--profile tokens-v2"); the V1 read/transfer path needs no overlay.

Quick start (on a running instance):
  token create  --instance <i> --endpoint <p> --non-interactive --name … --symbol …
  token mint    --instance <i> --endpoint <p> --instrument <sym> --to <party> --amount …
  token balances --instance <i> --endpoint <p>          # everyone's holdings at a glance
  token party new <alias> --instance <i> --endpoint <p> # name a party once, use it everywhere

[HoldingV2]: https://github.com/canton-network/splice/blob/token-standard-v2-upcoming/token-standard/splice-api-token-holding-v2/daml/Splice/Api/Token/HoldingV2.daml`,
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
	cmd.AddCommand(buildIdentity())
	cmd.AddCommand(buildFaucet())
	cmd.AddCommand(buildAllocate())
	cmd.AddCommand(buildAllocations())
	cmd.AddCommand(buildTransfers())
	return cmd
}

// errSilent makes cobra exit non-zero without its usage-and-error dump
// (the user already saw the remediation on stderr). The empty Error()
// string makes cobra's default renderer a no-op.
var errSilent silentError

type silentError struct{}

func (silentError) Error() string { return "" }
