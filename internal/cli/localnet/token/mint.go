package token

import (
	"errors"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

func buildMint() *cobra.Command {
	var opts token.MintOptions
	cmd := &cobra.Command{
		Use:   "mint",
		Short: "Mint additional supply of a V2 instrument to a party",
		Long: `Mint --amount of an instrument (resolved by --instrument as symbol or
raw id) into --to's account on the V2 LocalNet. Issuer-only; the
underlying Daml choice (BurnMintV1.Mint) refuses non-issuer submitters.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := token.RunMint(cmd.Context(), cmd.OutOrStdout(), opts)
			if err != nil && !errors.Is(err, token.ErrNeedsV2LocalNet) {
				return err
			}
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Instrument symbol or raw id. Required.")
	cmd.Flags().StringVar(&opts.To, "to", "", "Recipient party id. Required.")
	cmd.Flags().StringVar(&opts.Amount, "amount", "", "Decimal amount to mint. Required.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Required for the live asset-specific mint (test-token instruments created on-ledger).")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the mint.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("instrument")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("amount")
	return cmd
}
