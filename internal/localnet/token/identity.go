package token

import "github.com/bitdynamics-ab/canton-devkit/internal/splice"

// DefaultRole is the act-as identity every token surface falls back to
// when no role is chosen. Matches the CLI's --role default and the Web
// UI's roleFromQuery / DEFAULT_ROLE, so live-ledger endpoint discovery
// and JWT minting pick the same participant.
const DefaultRole = string(splice.RoleAppUser)

// Roles returns the act-as identities a LocalNet bring-up issues tokens
// for, in UI presentation order — the default (app-user) first. Both the
// CLI `token identity` verb and the Web UI's identity switcher render
// this list, so the two surfaces expose the same set in the same order.
//
// splice.AllRoles() is the underlying source of truth; this reorders it
// so the default leads (AllRoles lists sv first, which is the wrong lead
// for an act-as picker).
func Roles() []string {
	return []string{
		string(splice.RoleAppUser),
		string(splice.RoleAppProvider),
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
