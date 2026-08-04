package token

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildParty is the `token party` command group: the party alias
// registry. On LocalNet you own every party, so the workspace lets you
// allocate one by a readable alias and refer to it everywhere instead of
// pasting 130-char party ids. CLI counterpart of the Web UI party manager.
func buildParty() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "party",
		Short: "Manage party aliases for the instance workspace",
		Long: `Allocate and name parties on the LocalNet instance. An alias (e.g.
"bob") maps to an allocated on-ledger party id and can be used anywhere a
party is accepted (--to / --from / --party). On LocalNet there is no trust
boundary between parties — the dev secret signs for all of them — so this
is a single god-mode workspace, not a wallet-per-party.`,
	}
	cmd.AddCommand(buildPartyNew())
	cmd.AddCommand(buildPartyList())
	cmd.AddCommand(buildPartyRemove())
	return cmd
}

func buildPartyNew() *cobra.Command {
	var opts token.PartyOptions
	var format string
	cmd := &cobra.Command{
		Use:   "new <alias>",
		Short: "Allocate a party and register it under an alias",
		Long: `Allocate a fresh party on the role's participant (party-id hint =
alias), grant the role's user act/read-as rights, and record the alias.
The alias then resolves transparently in token commands and appears in the
balance matrix and activity feed.

--endpoint is optional: empty auto-resolves from the instance, like the
Web UI party manager.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Alias = args[0]
			ep, err := resolveEndpoint(cmd, opts.Instance, opts.Role, opts.Endpoint)
			if err != nil {
				return err
			}
			opts.Endpoint = ep
			ref, err := token.RunPartyNew(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(ref)
			}
			_, _ = fmt.Fprintf(out, "Registered party %q → %s (role %s).\n", ref.Alias, ref.PartyID, ref.Role)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Empty auto-resolves from the instance.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose participant hosts the new party.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}

func buildPartyList() *cobra.Command {
	var opts token.PartyOptions
	var format string
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List registered party aliases",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Best-effort endpoint resolution, matching the Web UI (which
			// always supplies a live endpoint so RunPartyList can lazy-seed
			// the role parties). When no port is captured we leave it empty
			// and fall back to the registry-only list. Explicit --endpoint wins.
			if opts.Endpoint == "" {
				opts.Endpoint = token.ResolveLedgerEndpoint(opts.Instance, opts.Role)
			}
			parties, err := token.RunPartyList(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(parties)
			}
			if len(parties) == 0 {
				_, _ = fmt.Fprintln(out, "No registered parties. Add one with `token party new <alias>`.")
				return nil
			}
			cols := []term.Column{{Label: "ALIAS"}, {Label: "ROLE"}, {Label: "PARTY ID"}}
			body := make([][]string, 0, len(parties))
			for _, p := range parties {
				body = append(body, []string{p.Alias, p.Role, p.PartyID})
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "Instance name. Required.")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", "", "Participant gRPC endpoint (host:port). Empty best-effort auto-resolves; seeds role parties when reachable.")
	cmd.Flags().StringVar(&opts.Token, "token", "", "Bearer JWT. Empty auto-issues a per-role token.")
	cmd.Flags().StringVar(&opts.Role, "role", "app-user", "Role whose participant to seed from.")
	cmd.Flags().BoolVar(&opts.Insecure, "insecure", true, "Use plaintext gRPC (LocalNet default).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}

func buildPartyRemove() *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:     "rm <alias>",
		Aliases: []string{"remove"},
		Short:   "Forget a party alias (does not de-allocate the on-ledger party)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := token.RunPartyRemove(instance, args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Forgot alias %q.\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name. Required.")
	_ = cmd.MarkFlagRequired("instance")
	return cmd
}
