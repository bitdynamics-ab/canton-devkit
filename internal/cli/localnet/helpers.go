package localnet

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// resolveName extracts the instance name from either a positional
// argument (args[0]) or the --name flag. Returns an error if both
// are provided (ambiguous) or neither is provided.
//
// This supports the UX shorthand:
//
//	localnet up mynet            # positional
//	localnet up --name mynet     # flag (backward compatible)
//	localnet up mynet --name x   # error: ambiguous
//	localnet up                  # error: name required
func resolveName(cmd *cobra.Command, args []string) (string, error) {
	// An empty/whitespace positional (e.g. `up ""`) counts as not
	// provided, so it falls through to the generic "name required" message
	// rather than a --name-flavored validation error from a blank name.
	hasPos := len(args) > 0 && strings.TrimSpace(args[0]) != ""
	hasFlag := cmd.Flags().Changed("name")
	switch {
	case hasPos && hasFlag:
		return "", fmt.Errorf("provide the instance name as an argument OR --name, not both")
	case hasPos:
		return args[0], nil
	case hasFlag:
		name, _ := cmd.Flags().GetString("name")
		return name, nil
	default:
		return "", fmt.Errorf("instance name required: `localnet %s <name>` or `localnet %s --name <name>`", cmd.Name(), cmd.Name())
	}
}
