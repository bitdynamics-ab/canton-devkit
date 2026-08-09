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

  • live ACS query against the participant (the default whenever the
    instance has a captured ledger port — same auto-discovery the Web
    UI uses). Filters by the V2 Holding interface
    (#splice-api-token-holding-v2:Splice.Api.Token.HoldingV2:Holding),
    parses each HoldingViewV2 record, and sums amounts per
    (party, instrument). Pass --endpoint host:port to dial a specific
    participant, and --token / --insecure to override auth/transport.

  • registry-derived pseudo-balance: used only when no live participant
    endpoint is available (the instance pre-dates port capture, or it's
    down). The issuer shows the full InitialSupply; everyone else shows
    0. These rows are clearly tagged source=registry — they are LOCAL
    bookkeeping, not on-ledger truth.

The SOURCE column (and the JSON "source" field) tells you which path a
row came from. With --party, filter to a single party; with
--instrument, filter to a single instrument (by symbol or raw id).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, truncated, err := token.RunBalance(cmd.Context(), nil, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if truncated {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: result truncated after %d holdings; refine --party or --instrument to see the rest\n",
					10_000)
			}
			// Response-level provenance: "ledger" when we summed a live
			// ACS, "registry" when we fell back to the pseudo-balance.
			src := token.BalanceSource(opts)
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				// Shared, schema-pinned envelope identical to the Web UI's
				// GET /api/tokens/{symbol}/holdings — a bare array has no
				// slot to version the shape or carry provenance.
				return enc.Encode(types.TokenHoldingsResponse{
					SchemaVersion: types.SchemaVersion,
					Source:        types.HoldingSource(src),
					Holdings:      toHoldings(rows),
					Truncated:     truncated,
				})
			}
			// The SOURCE column makes a registry pseudo-balance visibly
			// distinct from a real on-ledger holding.
			cols := []term.Column{
				{Label: "INSTRUMENT"},
				{Label: "PARTY"},
				{Label: "AMOUNT"},
				{Label: "SOURCE"},
			}
			body := make([][]string, 0, len(rows))
			for _, r := range rows {
				sym := r.InstrumentSymbol
				if sym == "" {
					sym = r.InstrumentID
				}
				body = append(body, []string{sym, r.Party, r.Amount, string(r.Source)})
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			if src == token.SourceRegistry {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"note: no live ledger reachable for this instance — amounts above are "+
						"registry pseudo-balances (issuer holds the full supply), not on-ledger holdings. "+
						"Start the instance to see real balances.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Party, "party", "", "Filter to a single party.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Filter to a single instrument (symbol or raw id).")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Defaults to the instance's captured ledger port; set to override which participant the live ACS query dials.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT for the participant. Empty (the common case) auto-issues a per-role token via `localnet creds` machinery.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-provider", "Role whose JWT the live ACS query authenticates as (sv / app-provider / app-user).")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}

// toHoldings converts the neutral token.BalanceRow slice into the
// shared api/types.TokenHolding wire shape so the CLI's --format json
// emits the exact same body as the Web UI's holdings endpoint.
func toHoldings(rows []token.BalanceRow) []types.TokenHolding {
	out := make([]types.TokenHolding, 0, len(rows))
	for _, r := range rows {
		out = append(out, types.TokenHolding{
			InstrumentSymbol: r.InstrumentSymbol,
			InstrumentID:     r.InstrumentID,
			Party:            r.Party,
			Amount:           r.Amount,
			Source:           types.HoldingSource(r.Source),
		})
	}
	return out
}
