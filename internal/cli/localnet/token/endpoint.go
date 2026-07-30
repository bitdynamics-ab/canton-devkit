package token

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// resolveEndpoint fills a token verb's ledger endpoint from the instance
// when the user didn't pass --endpoint — matching the Web UI, which never
// asks for an endpoint: it resolves the instance's captured participant
// port itself (liveLedgerEndpoint). An explicit --endpoint always wins.
//
// This keeps every token verb usable flag-free on a running LocalNet
// (`token <verb> --instance foo`), so the CLI and the Web UI stay at
// parity — a command the UI runs without an endpoint the CLI must too.
//
// When the port can't be resolved (never captured), it prints the same
// diagnosis the Web UI's 503 gives and returns errSilent so cobra exits
// non-zero without dumping usage.
func resolveEndpoint(cmd *cobra.Command, instance, role, endpoint string) (string, error) {
	if endpoint != "" {
		return endpoint, nil
	}
	ep := token.ResolveLedgerEndpoint(instance, role)
	if ep == "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"no captured ledger port for instance %q — restart it so ports are recorded, or pass --endpoint host:port\n",
			instance)
		return "", errSilent
	}
	return ep, nil
}
