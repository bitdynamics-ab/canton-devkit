package token

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildList returns `token ls` — list the instance's token instruments,
// the CLI counterpart of the Web UI instrument panel (GET /api/tokens).
// With a live ledger it is on-chain ACS discovery (so Amulet and every
// minted token appear); offline it falls back to the instruments recorded
// at create. The live-then-recorded decision and the JSON shape are shared
// with the handler via token.ListInstruments — this command only resolves
// the endpoint and renders the text tables.
func buildList() *cobra.Command {
	var opts token.BalanceOptions
	var format string
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list", "instruments"},
		Short:   "List the token instruments on the instance",
		Long: `List every token instrument visible on the instance — Amulet plus any
created or minted token. With a live ledger this is on-chain ACS discovery;
offline it falls back to the instruments recorded at create, mirroring the
Web UI instrument list.

--endpoint is optional: empty auto-resolves from the instance.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			// Best-effort resolve (like create and the Web UI): a live
			// endpoint gives on-chain discovery; empty falls back to the
			// recorded list rather than erroring. Explicit --endpoint wins.
			if opts.Endpoint == "" {
				opts.Endpoint = token.ResolveLedgerEndpoint(opts.Instance, opts.Role)
			}
			resp, err := token.ListInstruments(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}
			// A non-nil Instruments slice marks the live path — the same
			// "instruments present" discriminator the Web UI keys on.
			if resp.Instruments != nil {
				return renderInstruments(out, resp.Instruments)
			}
			return renderRecorded(out, resp.Tokens)
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Empty auto-resolves from the instance.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the scan.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}

// renderInstruments prints the live on-chain discovery result as a text table.
func renderInstruments(out io.Writer, insts []types.InstrumentRef) error {
	if len(insts) == 0 {
		_, _ = fmt.Fprintln(out, "No instruments.")
		return nil
	}
	cols := []term.Column{{Label: "SYMBOL"}, {Label: "NAME"}, {Label: "STANDARD"}, {Label: "ADMIN"}, {Label: "ON-LEDGER"}}
	body := make([][]string, 0, len(insts))
	for _, it := range insts {
		sym := it.Symbol
		if sym == "" {
			sym = it.InstrumentID
		}
		body = append(body, []string{sym, it.Name, it.Standard, shortParty(it.Admin), yesNo(it.OnLedger)})
	}
	_, _ = fmt.Fprintln(out, term.Table(cols, body))
	return nil
}

// renderRecorded prints the offline fallback (registry-recorded refs) as a text table.
func renderRecorded(out io.Writer, refs []types.TokenRef) error {
	if len(refs) == 0 {
		_, _ = fmt.Fprintln(out, "No instruments. Create one with `token create`.")
		return nil
	}
	cols := []term.Column{{Label: "SYMBOL"}, {Label: "NAME"}, {Label: "STATUS"}, {Label: "DECLARED"}}
	body := make([][]string, 0, len(refs))
	for _, t := range refs {
		body = append(body, []string{t.Symbol, t.Name, t.Status, t.InitialSupply})
	}
	_, _ = fmt.Fprintln(out, term.Table(cols, body))
	return nil
}
