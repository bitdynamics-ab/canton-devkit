package token

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

func buildBalance() *cobra.Command {
	var opts token.BalanceOptions
	var format string
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "List V2 token balances on the selected LocalNet",
		Long: `Print balances of recorded V2 instruments. With --party, filter to a
single party; with --instrument, filter to a single instrument (by
symbol or raw id). Output text or json.

Today balances are derived from the per-instance Tokens registry the
` + "`token create`" + ` wizard populates (the issuer party holds the
full InitialSupply; others show zero). The full ACS-derived balance
— a live count of Holding-interface contracts per (party, instrument)
on the participant — lands once the live V2 wiring is in.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, err := token.RunBalance(cmd.Context(), nil, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			// Text: simple aligned table via term.
			cols := []term.Column{
				{Label: "INSTRUMENT"},
				{Label: "PARTY"},
				{Label: "AMOUNT"},
			}
			body := make([][]string, 0, len(rows))
			for _, r := range rows {
				sym := r.InstrumentSymbol
				if sym == "" {
					sym = r.InstrumentID
				}
				body = append(body, []string{sym, r.Party, r.Amount})
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Party, "party", "", "Filter to a single party.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Filter to a single instrument (symbol or raw id).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}
