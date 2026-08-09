package token

import "github.com/bitdynamics-ab/canton-devkit/internal/splice"

// DefaultRole is the act-as identity every token surface falls back to
// when no role is chosen. Matches the CLI's --role default and the Web
// UI's roleFromQuery / DEFAULT_ROLE, so live-ledger endpoint discovery
// and JWT minting pick the same participant.
const DefaultRole = string(splice.RoleAppProvider)

// Roles returns the act-as identities a LocalNet issues tokens for, in
// UI order with the default (app-provider) first. Shared by the CLI
// `token identity` verb and the Web UI switcher so both list the same
// set in the same order. Reorders splice.AllRoles() (which leads with sv).
func Roles() []string {
	return []string{
		string(splice.RoleAppProvider),
		string(splice.RoleAppUser),
		string(splice.RoleSV),
	}
}

// ValidRole reports whether role is one of the known act-as identities.
func ValidRole(role string) bool {
	for _, r := range Roles() {
		if r == role {
			return true
		}
	}
	return false
}
