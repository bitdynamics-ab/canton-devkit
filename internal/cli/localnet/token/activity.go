package token

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildActivity is `token activity` — an instrument's transfer/mint/burn
// history, reconstructed from HoldingV2 create/archive events on the
// ledger transaction stream. CLI counterpart of the Web UI activity feed.
func buildActivity() *cobra.Command {
	var opts token.BalanceOptions
	var format string
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show an instrument's transfer/mint/burn history",
		Long: `Scan the ledger transaction stream and reconstruct a single
instrument's activity: each transaction netted into who sent how much to
whom, classified as mint (new supply), burn (supply destroyed), or
transfer. Derived from HoldingV2 create/archive events — no off-ledger
transfer-events registry needed.

Requires --endpoint (the participant ledger gRPC host:port) and
--instrument (the symbol or instrument id).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := token.RunActivityResult(cmd.Context(), opts)
			if err != nil {
				return err
			}
			events := res.Events
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			if res.Truncated {
				_, _ = fmt.Fprintln(out, "(activity stream truncated to bound memory; showing partial history)")
			}

			cols := []term.Column{
				{Label: "OFFSET"},
				{Label: "TIME"},
				{Label: "KIND"},
				{Label: "SOURCE"},
				{Label: "AMOUNT"},
				{Label: "FROM"},
				{Label: "TO"},
			}
			body := make([][]string, 0, len(events))
			for _, e := range events {
				body = append(body, []string{
					fmt.Sprintf("%d", e.Offset),
					e.RecordTime,
					e.Kind,
					sourceLabel(e.Source),
					e.Amount,
					partyList(e.Senders),
					partyList(e.Receivers),
				})
			}
			if len(body) == 0 {
				_, _ = fmt.Fprintln(out, "No activity for this instrument.")
				return nil
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Required.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Instrument symbol or id. Required.")
	cmd.Flags().IntVar(&opts.Limit, "limit", 50, "Max events to return (newest first).")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the scan.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("endpoint")
	_ = cmd.MarkFlagRequired("instrument")
	return cmd
}

// sourceLabel renders an ActivityEvent.Source for the text table:
// "EventLog" (admin's authoritative events) vs "derived" (HoldingV2 netting).
func sourceLabel(source string) string {
	if source == "event_log" {
		return "EventLog"
	}
	return "derived"
}

// partyList renders party deltas as "alice +100, bob +5" for the table.
func partyList(deltas []token.PartyDelta) string {
	if len(deltas) == 0 {
		return "·"
	}
	parts := make([]string, 0, len(deltas))
	for _, d := range deltas {
		parts = append(parts, fmt.Sprintf("%s %s", shortParty(d.Party), d.Amount))
	}
	return strings.Join(parts, ", ")
}
