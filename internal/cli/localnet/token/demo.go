package token

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// buildDemo returns `token demo`, a one-step launch of a transferable demo
// token. The Web UI's "Launch demo token" button drives the same
// token.RunDemo via POST /api/tokens/demo.
func buildDemo() *cobra.Command {
	var (
		instance   string
		role       string
		endpoint   string
		insecure   bool
		symbol     string
		supply     string
		decimals   int
		seedHolder bool
		format     string
	)
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Provision a live, transferable demo token in one step",
		Long: `Launch a transferable demo token end-to-end in a single command. The
flow adapts to the instance:

  token-standard-v2 instance: allocate an issuer party, create a V2
    instrument on-ledger, mint the initial supply, and (unless
    --seed-holder=false) fund a holder party.

  standard instance (V1): there is no create/mint — Amulet is the only
    instrument — so a holder party is funded with Amulet moved from the
    role's network-funded party, giving a transferable balance.

Requires a running LocalNet — the participant endpoint is auto-
discovered from the instance's captured port (pass --endpoint to
override). The Web UI's "Launch demo token" button runs the same
orchestration via POST /api/tokens/demo.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ep := endpoint
			if ep == "" {
				ep = token.ResolveLedgerEndpoint(instance, role)
			}
			res, err := token.RunDemo(cmd.Context(), out, token.DemoOptions{
				Instance:      instance,
				Role:          role,
				Endpoint:      ep,
				Insecure:      insecure,
				Symbol:        symbol,
				InitialSupply: supply,
				Decimals:      decimals,
				SeedHolder:    seedHolder,
			})
			if err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(res); err != nil {
					return fmt.Errorf("encode result: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&role, "role", "app-user", "Role whose JWT authenticates the on-ledger provisioning.")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Auto-discovered from the instance when empty.")
	cmd.Flags().BoolVar(&insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&symbol, "symbol", "DEMO", "Instrument symbol.")
	cmd.Flags().StringVar(&supply, "supply", "1000000", "Initial supply (decimal string).")
	cmd.Flags().IntVar(&decimals, "decimals", 6, "Decimal precision (0..18).")
	cmd.Flags().BoolVar(&seedHolder, "seed-holder", true, "Allocate a holder party and fund it so the token is transferable.")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json (json prints the DemoResult).")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}
