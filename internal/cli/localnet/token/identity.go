package token

import (
	"encoding/json"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildIdentity is the `token identity` verb: list the act-as identities
// (roles) the instance's token commands can run under, and echo the one
// --role selects. CLI counterpart of the Web UI Tokens screen's identity
// switcher — both render token.Roles() so the surfaces stay in lockstep.
//
// The per-verb --role flag on mint/transfer/etc. is where you actually
// switch identity; this just enumerates the choices (and, with --format
// json, feeds a script the current selection).
func buildIdentity() *cobra.Command {
	var instance, role, format string
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "List act-as identities (roles) for the instance's token commands",
		Long: `Show the roles a LocalNet bring-up issues tokens for — the identities
every token verb can act as via --role. The default identity is app-user,
matching the per-verb --role default and the Web UI's identity switcher.

Switching identity is done per command with --role; this verb enumerates
the available choices and (with --role) marks the active one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if role != "" && !token.ValidRole(role) {
				return fmt.Errorf("--role must be one of %v (got %q)", token.Roles(), role)
			}
			if role == "" {
				role = token.DefaultRole
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(types.TokenIdentityResponse{
					SchemaVersion:  types.SchemaVersion,
					Instance:       instance,
					AvailableRoles: token.Roles(),
					CurrentRole:    role,
				})
			}
			cols := []term.Column{{Label: "ROLE"}, {Label: "ACTIVE"}}
			body := make([][]string, 0, len(token.Roles()))
			for _, r := range token.Roles() {
				active := ""
				if r == role {
					active = "*"
				}
				body = append(body, []string{r, active})
			}
			_, _ = fmt.Fprintln(out, term.Table(cols, body))
			return nil
		},
	}
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name (echoed in --format json).")
	cmd.Flags().StringVar(&role, "role", "", "Active identity to mark (default app-user).")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json.")
	return cmd
}
