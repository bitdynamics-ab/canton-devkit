package token

import (
	"errors"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// buildSettle returns `token settle` — the executor-side of a V2 DvP
// settlement. Fetches the settlement-factory context and exercises
// SettlementFactory_SettleBatch for the batch a finalized Allocation
// belongs to. Web UI counterpart: POST /api/tokens/allocations/{id}/settle.
//
// TODO: end-to-end settlement is not yet proven; RunSettle resolves the
// settlement-factory context and returns a clear not-proven error. See
// internal/localnet/token/run_allocation.go RunSettle for the why.
func buildSettle() *cobra.Command {
	var opts token.AllocationActionOptions
	cmd := &cobra.Command{
		Use:   "settle",
		Short: "Settle a V2 DvP allocation batch (executor)",
		Long: `Settle the settlement batch a finalized Allocation belongs to, as the
executor: exercise SettlementFactory_SettleBatch to atomically move every
leg of the batch.

Note: end-to-end settlement is not yet proven on LocalNet — there is no
registry settle choice-context endpoint (settlement is executor-driven via
the settlement-factory context), and the batch assembly needs a live V2
instance to validate against. This resolves the settlement-factory context
(proving the endpoint wiring) and reports the remaining step.

Requires --endpoint (participant ledger gRPC host:port) and --allocation.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := token.RunSettle(cmd.Context(), cmd.OutOrStdout(), opts)
			if errors.Is(err, token.ErrNeedsV2LocalNet) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSilent
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.AllocationID, "allocation", "", "Allocation contract id whose settlement batch to settle. Required.")
	cmd.Flags().StringVar(&opts.Party, "party", "", "Executor party. Defaults to the JWT's first granted party.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). When set, run live; otherwise print the not-wired remediation.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose JWT authenticates the submit.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&opts.RegistryURL, "registry-url", "", "Token registry base URL. Empty auto-derives from the instance's SV UI port.")
	_ = cmd.MarkFlagRequired("instance")
	_ = cmd.MarkFlagRequired("allocation")
	return cmd
}
