package token

import (
	"errors"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// buildTransfer returns the `token transfer` parent + its `accept`
// sub-subcommand. Models the upstream Splice CLI split (which has
// separate `transfer` and `acceptTransferInstruction` commands) so a
// V2 wallet flow lines up 1:1 with our surface.
func buildTransfer() *cobra.Command {
	parent := &cobra.Command{
		Use:   "transfer",
		Short: "Initiate / accept a V2 token transfer",
		Long: `Initiate a V2 transfer (sender command, default action) or accept a
pending TransferInstruction as the receiver (` + "`transfer accept`" + `
subcommand). The two-step flow is the upstream V2 default — direct
and self transfers are surfaced automatically when the registry's
TransferFactory response indicates the sender pre-approved them.`,
		Args: cobra.NoArgs,
	}
	parent.RunE = func(cmd *cobra.Command, _ []string) error {
		// Inline initiator (no subcommand): runs RunTransfer.
		var opts token.TransferOptions
		opts.Instance, _ = cmd.Flags().GetString("instance")
		opts.Instrument, _ = cmd.Flags().GetString("instrument")
		opts.From, _ = cmd.Flags().GetString("from")
		opts.To, _ = cmd.Flags().GetString("to")
		opts.Amount, _ = cmd.Flags().GetString("amount")
		opts.NoWait, _ = cmd.Flags().GetBool("no-wait")
		opts.Reason, _ = cmd.Flags().GetString("reason")
		err := token.RunTransfer(cmd.Context(), cmd.OutOrStdout(), opts)
		if errors.Is(err, token.ErrNeedsV2LocalNet) {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
			return errSilent
		}
		return err
	}
	parent.Flags().String("instance", "", "Instance name. Required.")
	parent.Flags().String("instrument", "", "Instrument symbol or raw id. Required.")
	parent.Flags().String("from", "", "Sender party id. Required.")
	parent.Flags().String("to", "", "Receiver party id. Required.")
	parent.Flags().String("amount", "", "Decimal amount. Required.")
	parent.Flags().Bool("no-wait", false, "Return the TransferInstruction id without waiting for the receiver to accept.")
	parent.Flags().String("reason", "", "Optional human-readable reason recorded on the TransferInstruction.")
	_ = parent.MarkFlagRequired("instance")
	// Don't mark every flag required at the parent — `transfer accept`
	// shares the parent but only uses --instance + --id.

	parent.AddCommand(buildTransferAccept())
	return parent
}

func buildTransferAccept() *cobra.Command {
	var opts token.AcceptOptions
	cmd := &cobra.Command{
		Use:   "accept",
		Short: "Accept a pending V2 TransferInstruction as the receiver",
		Long: `Receiver counterpart to ` + "`transfer`" + `. Fetches the AcceptTransfer
choice context from the token registry HTTP API and exercises the
choice on the Ledger API. Idempotent on the registry side — accepting
twice is rejected by the underlying Daml choice.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := token.RunAccept(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.TransferInstructionID, "id", "", "TransferInstruction contract id. Required.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}
