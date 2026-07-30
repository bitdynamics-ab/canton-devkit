package token

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildList returns `token ls` — list the instance's token instruments,
// the CLI counterpart of the Web UI instrument panel (GET /api/tokens).
// With a live ledger it is on-chain ACS discovery (so Amulet and every
// minted token appear); offline it falls back to the instruments recorded
// at create. Mirrors the handler's live-then-recorded logic and JSON keys.
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
			// Best-effort resolve (like create): a live endpoint gives
			// on-chain discovery; when it can't be resolved we fall back to
			// the recorded list rather than erroring — exactly as the Web
			// UI's instrument panel does.
			if opts.Endpoint == "" {
				opts.Endpoint = token.ResolveLedgerEndpoint(opts.Instance, opts.Role)
			}
			if opts.Endpoint != "" {
				if insts, err := token.RunInstruments(cmd.Context(), opts); err == nil {
					return renderInstruments(cmd, format, insts)
				}
				// Discovery failed (ledger momentarily unreachable) — fall
				// through to the recorded list rather than erroring.
			}
			refs, err := token.ListTokens(opts.Instance)
			if err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				// Same key ("tokens") as the handler's recorded-list branch.
				return enc.Encode(map[string]any{
					"schema_version": types.SchemaVersion,
					"tokens":         refs,
				})
			}
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

// renderInstruments prints the on-chain discovery result — JSON under the
// "instruments" key (matching GET /api/tokens) or a text table.
func renderInstruments(cmd *cobra.Command, format string, insts []token.InstrumentRef) error {
	out := cmd.OutOrStdout()
	if format == "json" {
		if insts == nil {
			insts = []token.InstrumentRef{}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema_version": types.SchemaVersion,
			"instruments":    insts,
		})
	}
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
