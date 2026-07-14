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
		opts.AutoAccept, _ = cmd.Flags().GetBool("auto-accept")
		opts.Atomic, _ = cmd.Flags().GetBool("atomic")
		opts.Reason, _ = cmd.Flags().GetString("reason")
		opts.Endpoint, _ = cmd.Flags().GetString("endpoint")
		opts.Token, _ = cmd.Flags().GetString("token")
		opts.Role, _ = cmd.Flags().GetString("role")
		opts.Insecure, _ = cmd.Flags().GetBool("insecure")
		opts.RegistryURL, _ = cmd.Flags().GetString("registry-url")
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
	parent.Flags().Bool("auto-accept", false, "Chain the receiver-side accept onto the transfer (LocalNet: you own the receiver, so settle in one step).")
	parent.Flags().Bool("atomic", false, "(experimental) With --auto-accept, batch the transfer and receiver-accept into one all-or-nothing BatchingUtilityV2 transaction (on-ledger test tokens only). Not yet supported on current Splice: the accept leg can't reference the transfer leg's instruction within one batch, so this errors and nothing commits — use the default sequential path.")
	parent.Flags().String("reason", "", "Optional human-readable reason recorded on the TransferInstruction.")
	parent.Flags().String("endpoint", "", "Participant gRPC endpoint (host:port). When set, run the live V2 transfer; otherwise print the not-wired remediation.")
	parent.Flags().String("token", "", "Bearer JWT. Empty auto-issues a per-role token via the creds machinery.")
	parent.Flags().String("role", "app-user", "Role whose JWT authenticates the submit (sv / app-provider / app-user).")
	parent.Flags().Bool("insecure", true, "Use plaintext gRPC (LocalNet default).")
	parent.Flags().String("registry-url", "", "Token registry base URL. Empty auto-derives the LocalNet scan registry from the instance's SV UI port.")
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
	cmd.Flags().StringVar(&opts.Party, "party", "", "Receiver party acting on the instruction. Defaults to the JWT's first granted party (set explicitly on multi-party participants).")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). When set, run the live accept; otherwise print the not-wired remediation.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the submit.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&opts.RegistryURL, "registry-url", "", "Token registry base URL. Empty auto-derives from the instance's SV UI port.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}
