package token

import (
	"errors"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

func buildBurn() *cobra.Command {
	var opts token.BurnOptions
	cmd := &cobra.Command{
		Use:   "burn",
		Short: "Burn a holding of a V2 instrument",
		Long: `Burn --amount from --from's holding of --instrument. V2 reuses the V1
BurnMint choice for the burn side; this command wraps the same flow
the upstream Splice CLI uses.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := token.RunBurn(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Instrument symbol or raw id. Required.")
	cmd.Flags().StringVar(&opts.From, "from", "", "Holder party id. Required.")
	cmd.Flags().StringVar(&opts.Amount, "amount", "", "Decimal amount to burn. Required.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("instrument")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("amount")
	return cmd
}
