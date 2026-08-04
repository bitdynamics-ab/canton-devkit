package token

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// buildAllocate returns `token allocate` — the authorizer-side of a V2
// DvP allocation. Picks the authorizer's holdings, POSTs the registry
// allocation-factory, and exercises AllocationFactory_Allocate, producing
// a finalized Allocation (or a pending AllocationInstruction) the executor
// later settles. Web UI counterpart: POST /api/tokens/{symbol}/allocate.
func buildAllocate() *cobra.Command {
	var opts token.AllocationOptions
	var format string
	cmd := &cobra.Command{
		Use:   "allocate",
		Short: "Allocate holdings to a V2 settlement (DvP)",
		Long: `Create a V2 allocation: lock the authorizer's holdings behind an
approval to move them as one leg of a settlement batch. The allocation is
either finalized immediately (a ready-to-settle Allocation) or pending
(an AllocationInstruction the registry must accept first).

A committed allocation (--committed) locks the funds until
--settlement-deadline passes — they cannot be withdrawn early. A
non-committed allocation is withdrawable any time.

--endpoint is optional (empty auto-resolves from the instance). Batch
settlement (SettleBatch) is not yet functional on LocalNet, so there is no
settle command on either surface — release an allocation with
` + "`token allocations withdraw|cancel`" + ` instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ep, err := resolveEndpoint(cmd, opts.Instance, opts.Role, opts.Endpoint)
			if err != nil {
				return err
			}
			opts.Endpoint = ep
			id, err := token.RunAllocate(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			if err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"allocation_id": id})
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Instrument, "instrument", "", "Instrument symbol or raw id. Required.")
	cmd.Flags().StringVar(&opts.From, "from", "", "Authorizer party (funds the transfer leg). Required.")
	cmd.Flags().StringVar(&opts.To, "to", "", "Receiver party of the transfer leg. Required.")
	cmd.Flags().StringVar(&opts.Amount, "amount", "", "Decimal amount to allocate. Required.")
	cmd.Flags().StringVar(&opts.Executor, "executor", "", "Settlement executor party. Empty defaults to --from (LocalNet: you own everyone).")
	cmd.Flags().StringVar(&opts.SettlementDeadline, "settlement-deadline", "", "Deadline as RFC3339 or a duration (e.g. 1h). A committed allocation locks funds until it passes.")
	cmd.Flags().BoolVar(&opts.Committed, "committed", false, "Commit the allocation: lock funds until the settlement deadline (no early withdraw).")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). When set, run the live allocate; otherwise print the not-wired remediation.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the submit.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&opts.RegistryURL, "registry-url", "", "Token registry base URL. Empty auto-derives from the instance's SV UI port.")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json (json prints the allocation id).")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("instrument")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("amount")
	return cmd
}
