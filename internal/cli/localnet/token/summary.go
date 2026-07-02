package token

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildSummary is `token summary` — the instrument-first KPI view. One
// ACS scan yields total supply, holder + holding-contract counts, and
// the per-holder distribution with share-of-supply. CLI counterpart of
// the Web UI TokenDetail KPI strip.
func buildSummary() *cobra.Command {
	var opts token.BalanceOptions
	var format string
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Show supply, holder, and holding-contract KPIs for one instrument",
		Long: `Scan the live ACS once and summarise a single instrument: total
supply (= circulating, on a UTXO ledger), number of holders, number of
Holding contracts (UTXOs), and the per-holder distribution with each
holder's share of supply.

Requires --endpoint (the participant ledger gRPC host:port) and
--instrument (the symbol or instrument id).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := token.RunInstrumentSummary(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}

			_, _ = fmt.Fprintf(out, "Instrument   %s\n", s.InstrumentID)
			_, _ = fmt.Fprintf(out, "Admin        %s\n", shortParty(s.Admin))
			_, _ = fmt.Fprintf(out, "Supply       %s\n", s.TotalSupply)
			_, _ = fmt.Fprintf(out, "Holders      %d\n", s.HolderCount)
			_, _ = fmt.Fprintf(out, "Contracts    %d (UTXOs)\n\n", s.ContractCount)

			cols := []term.Column{
				{Label: "HOLDER"},
				{Label: "BALANCE"},
				{Label: "SHARE"},
				{Label: "UTXOS"},
			}
			body := make([][]string, 0, len(s.Holders))
			for _, h := range s.Holders {
				body = append(body, []string{
					shortParty(h.Party),
					h.Balance,
					h.PctOfSupply + "%",
					fmt.Sprintf("%d", h.ContractCount),
				})
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Required.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Instrument symbol or id. Required.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the scan.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("endpoint")
	_ = cmd.MarkFlagRequired("instrument")
	return cmd
}
