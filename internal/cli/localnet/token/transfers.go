package token

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildTransfers returns `token transfers` — list the pending V2 transfer
// offers (an ACS scan of the TransferInstruction interface). Every active
// TransferInstruction is a still-pending offer, since acceptance archives
// it. Web UI counterpart: GET /api/tokens/transfers. The rows carry the
// contract id `transfer accept --id` needs.
func buildTransfers() *cobra.Command {
	var opts token.ListPendingTransfersOptions
	var format string
	cmd := &cobra.Command{
		Use:   "transfers",
		Short: "List pending transfer offers",
		Long: `List the pending V2 transfer offers visible on this participant —
TransferInstruction contracts the receiver has not yet accepted —
optionally filtered with --party (matches either the sender or the
receiver). Each row's OFFER id is what 'transfer accept --id' takes.

--endpoint defaults to the instance's captured ledger port; pass it to
dial a different participant.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ep, err := resolveEndpoint(cmd, opts.Instance, opts.Role, opts.Endpoint)
			if err != nil {
				return err
			}
			opts.Endpoint = ep
			rows, truncated, err := token.RunListPendingTransfers(cmd.Context(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				if rows == nil {
					rows = []types.TransferSummary{}
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				// Same top-level shape as GET /api/tokens/transfers so CLI
				// JSON and Web UI cannot drift.
				return enc.Encode(types.PendingTransfersResponse{
					SchemaVersion:    types.SchemaVersion,
					PendingTransfers: rows,
					Truncated:        truncated,
					Aliases:          token.PartyAliasMap(opts.Instance),
				})
			}
			if len(rows) == 0 {
				_, _ = fmt.Fprintln(out, "No pending transfer offers.")
				return nil
			}
			cols := []term.Column{
				{Label: "OFFER"},
				{Label: "FROM"},
				{Label: "TO"},
				{Label: "INSTRUMENT"},
				{Label: "AMOUNT"},
				{Label: "EXPIRES"},
			}
			body := make([][]string, 0, len(rows))
			for _, t := range rows {
				body = append(body, []string{
					shortParty(t.ContractID),
					shortParty(t.Sender),
					shortParty(t.Receiver),
					t.InstrumentID,
					t.Amount,
					t.ExecuteBefore,
				})
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			if truncated {
				_, _ = fmt.Fprintf(out, "\n(showing the first %d — more offers exist)\n", len(rows))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Party, "party", "", "Filter to offers involving one party (alias or id). Empty lists all visible.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Empty auto-resolves from the instance.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-provider", "Role whose JWT authenticates the scan.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}
