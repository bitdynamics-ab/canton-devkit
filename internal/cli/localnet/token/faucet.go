package token

import (
	"errors"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// buildFaucet is `token faucet <party> <amount>`: fund a party from a
// well-known source in one step, auto-accepted. A thin wrapper over the
// transfer engine — no new ledger primitive. CLI counterpart of the
// Web UI faucet action.
func buildFaucet() *cobra.Command {
	var opts token.FaucetOptions
	cmd := &cobra.Command{
		Use:   "faucet <party> <amount>",
		Short: "Fund a party from a well-known source (auto-accepted)",
		Long: `Transfer <amount> of an instrument from a funded source party to
<party>, auto-accepting the resulting TransferInstruction so the target is
funded in one step. The source defaults to the role's own funded party
(e.g. app-user holds the network's Amulet) — pass --source to fund from a
different holder, such as a created token's issuer.

<party> and --source accept aliases. Requires --endpoint and --instrument.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.To = args[0]
			opts.Amount = args[1]
			err := token.RunFaucet(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Instrument symbol or raw id. Required.")
	cmd.Flags().StringVar(&opts.Source, "source", "", "Funding party (alias or id). Empty defaults to the role's own funded party.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Required for the live transfer.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the submit.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&opts.RegistryURL, "registry-url", "", "Token registry base URL. Empty auto-derives from the instance's SV UI port.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("instrument")
	_ = cmd.MarkFlagRequired("endpoint")
	return cmd
}
