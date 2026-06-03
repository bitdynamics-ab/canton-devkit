package localnet

import (
	"fmt"
	"strings"
)

// ValidateFormat checks that got is one of allowed and returns a
// consistent error message otherwise. Used by every CLI subcommand
// that takes a --format flag (env, list, status, doctor, container,
// refresh, dar/*) so the error wording matches across the surface.
//
// Mirrors internal/cli/localnet.ValidateFormat — defined here so
// the BIT-127 DAR commands (which import internal/localnet, not
// internal/cli/localnet) can share the same validation without an
// upward import.
func ValidateFormat(got string, allowed ...string) error {
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return fmt.Errorf("--format must be one of %s (got %q)",
		strings.Join(allowed, ","), got)
}
