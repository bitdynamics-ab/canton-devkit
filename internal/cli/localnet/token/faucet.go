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
funded in one step. The source defaults to the instrument's largest current
holder — the network's Amulet party for Amulet, or wherever a created
token's supply was minted — so pass --source only to fund from a specific
holder.

<party> and --source accept aliases. Requires --instrument; --endpoint is
optional (empty auto-resolves from the instance, like the Web UI).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.To = args[0]
			opts.Amount = args[1]
			resolved, epErr := resolveEndpoint(cmd, opts.Instance, opts.Role, opts.Endpoint)
			if epErr != nil {
				return epErr
			}
			opts.Endpoint = resolved.Endpoint
			opts.Role = resolved.Role
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
	cmd.Flags().StringVar(&opts.Source, "source", "", "Funding party (alias or id). Empty defaults to the instrument's largest holder.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Empty auto-resolves from the instance.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-provider", "Role whose JWT authenticates the submit.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&opts.RegistryURL, "registry-url", "", "Token registry base URL. Empty auto-derives from the instance's SV UI port.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("instrument")
	return cmd
}
