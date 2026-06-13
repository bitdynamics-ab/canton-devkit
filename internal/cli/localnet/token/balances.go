package token

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildBalances is the god-mode `token balances` (plural) command: the
// party × instrument matrix over the whole instance, in one ACS scan.
// Sibling to the single-party `token balance`. CLI counterpart of the
// Web UI HoldingsMatrix lens .
func buildBalances() *cobra.Command {
	var opts token.BalanceOptions
	var format string
	cmd := &cobra.Command{
		Use:   "balances",
		Short: "Show the party × instrument balance matrix for the instance",
		Long: `Scan the live ACS once and print every party's balance of every
instrument as a matrix (rows = parties, columns = instruments). The
god-mode reconciliation view — only the parties the role's JWT can read
are included.

Requires --endpoint (the participant ledger gRPC host:port).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := token.RunBalanceMatrix(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(m)
			}

			// Build a dense table: PARTY | <sym1> | <sym2> | ...
			syms := make([]string, 0, len(m.Instruments))
			symByInst := map[string]string{}
			for _, in := range m.Instruments {
				syms = append(syms, in.Symbol)
				symByInst[in.InstrumentID] = in.Symbol
			}
			// amount lookup keyed by party→symbol
			amt := map[string]map[string]string{}
			for _, c := range m.Cells {
				if amt[c.Party] == nil {
					amt[c.Party] = map[string]string{}
				}
				amt[c.Party][symByInst[c.InstrumentID]] = c.Amount
			}
			cols := []term.Column{{Label: "PARTY"}}
			for _, s := range syms {
				cols = append(cols, term.Column{Label: s})
			}
			parties := append([]string{}, m.Parties...)
			sort.Strings(parties)
			body := make([][]string, 0, len(parties)+1)
			for _, p := range parties {
				row := []string{shortParty(p)}
				for _, s := range syms {
					v := amt[p][s]
					if v == "" {
						v = "·"
					}
					row = append(row, v)
				}
				body = append(body, row)
			}
			// totals row
			totByInst := map[string]string{}
			for _, t := range m.Totals {
				totByInst[symByInst[t.InstrumentID]] = t.Amount
			}
			totRow := []string{"Σ total"}
			for _, s := range syms {
				totRow = append(totRow, totByInst[s])
			}
			body = append(body, totRow)
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Required.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the scan.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("endpoint")
	return cmd
}

// shortParty trims a fingerprinted party id to its readable prefix for
// table display: `bob::1220fa…` → `bob`.
func shortParty(p string) string {
	for i := 0; i < len(p)-1; i++ {
		if p[i] == ':' && p[i+1] == ':' {
			return p[:i]
		}
	}
	return p
}
