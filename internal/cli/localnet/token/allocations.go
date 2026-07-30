package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildAllocations returns `token allocations` — list V2 allocations (an
// ACS scan of the Allocation interface) plus the per-allocation
// `withdraw` / `cancel` action subcommands. Web UI counterparts:
// GET /api/tokens/allocations and POST /api/tokens/allocations/{id}/{withdraw,cancel}.
func buildAllocations() *cobra.Command {
	var opts token.ListAllocationsOptions
	var format string
	cmd := &cobra.Command{
		Use:   "allocations",
		Short: "List V2 DvP allocations",
		Long: `List the ready-to-settle V2 allocations visible on this participant,
optionally filtered to one authorizer with --party. Each row is one
finalized Allocation contract: its settlement id, authorizer, leg count,
and whether it is committed (funds locked until the settlement deadline).

--endpoint is optional: empty auto-resolves from the instance, like the
Web UI.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ep, err := resolveEndpoint(cmd, opts.Instance, opts.Role, opts.Endpoint)
			if err != nil {
				return err
			}
			opts.Endpoint = ep
			rows, err := token.RunListAllocations(cmd.Context(), opts)
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
					rows = []types.AllocationSummary{}
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				// Same top-level shape as GET /api/tokens/allocations so
				// CLI JSON and Web UI cannot drift.
				return enc.Encode(types.AllocationsResponse{
					SchemaVersion: types.SchemaVersion,
					Allocations:   rows,
					Aliases:       token.PartyAliasMap(opts.Instance),
				})
			}
			if len(rows) == 0 {
				_, _ = fmt.Fprintln(out, "No allocations.")
				return nil
			}
			cols := []term.Column{
				{Label: "ALLOCATION"},
				{Label: "SETTLEMENT"},
				{Label: "AUTHORIZER"},
				{Label: "LEGS"},
				{Label: "COMMITTED"},
				{Label: "STATUS"},
			}
			body := make([][]string, 0, len(rows))
			for _, a := range rows {
				body = append(body, []string{
					shortParty(a.ContractID),
					a.SettlementID,
					shortParty(a.Authorizer),
					fmt.Sprintf("%d", a.LegCount),
					yesNo(a.Committed),
					string(a.Status),
				})
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Party, "party", "", "Filter to one authorizer party (alias or id). Empty lists all visible.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Empty auto-resolves from the instance.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the scan.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")

	cmd.AddCommand(buildAllocationAction("withdraw",
		"Withdraw a V2 allocation (committed: only after its settlement deadline)",
		token.RunAllocationWithdraw))
	cmd.AddCommand(buildAllocationAction("cancel",
		"Cancel a V2 allocation",
		token.RunAllocationCancel))
	return cmd
}

// allocationActionFn is the shared RunX shape of the per-allocation
// withdraw / cancel / settle verbs.
type allocationActionFn func(context.Context, io.Writer, token.AllocationActionOptions) error

// buildAllocationAction builds a per-allocation action subcommand
// (withdraw / cancel) dispatching to the passed RunX function.
func buildAllocationAction(verb, short string, run allocationActionFn) *cobra.Command {
	var opts token.AllocationActionOptions
	cmd := &cobra.Command{
		Use:   verb,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := run(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.AllocationID, "allocation", "", "Allocation contract id. Required.")
	cmd.Flags().StringVar(&opts.Party, "party", "", "Party exercising the choice. Defaults to the JWT's first granted party.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). When set, run live; otherwise print the not-wired remediation.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the submit.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&opts.RegistryURL, "registry-url", "", "Token registry base URL. Empty auto-derives from the instance's SV UI port.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("allocation")
	return cmd
}

// yesNo renders a bool as yes/no for table cells.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
