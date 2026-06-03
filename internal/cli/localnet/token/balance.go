package token

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
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
		Long: `Print balances of V2 token instruments. Two paths:

  • --endpoint host:port (+ optional --token, --insecure): live ACS
    query against the participant. Filters by the V2 Holding
    interface (#splice-api-token-holding-v2:Splice.Api.Token.HoldingV2:
    Holding), parses each HoldingViewV2 record, and sums amounts per
    (party, instrument). The right path for any V2 LocalNet.

  • no --endpoint: fall back to the registry-derived pseudo-balance
    populated by the ` + "`token create`" + ` wizard (issuer holds the
    full InitialSupply; everyone else shows 0). Useful for the
    "show me what I've registered locally" case before the participant
    is dialed.

With --party, filter to a single party; with --instrument, filter to a
single instrument (by symbol or raw id).`,
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
				// Wire-stable envelope ({schema_version, rows}) for parity
				// with the HTTP /api/tokens responses — a bare array has no
				// slot to version the shape.
				return enc.Encode(map[string]any{
					"schema_version": types.SchemaVersion,
					"rows":           rows,
				})
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
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). When set, query live ACS; otherwise use the registry pseudo-balance.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT for the participant. Empty (the common case) auto-issues a per-role token via `localnet creds` machinery.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT the live ACS query authenticates as (sv / app-provider / app-user).")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}
