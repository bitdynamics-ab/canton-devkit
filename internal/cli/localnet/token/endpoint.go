package token

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/spf13/cobra"
)

// resolveLedgerEndpointFn is the narrow test seam for best-effort callers,
// whose fallback behavior otherwise hides whether resolution was attempted.
var resolveLedgerEndpointFn = token.ResolveLedgerEndpoint

func bestEffortEndpoint(instance, role, endpoint string) string {
	if endpoint != "" {
		return endpoint
	}
	if role == "" {
		role = token.DefaultRole
	}
	return resolveLedgerEndpointFn(instance, role)
}

// resolvedLedger is the shared resolution metadata every token verb
// surfaces after resolveEndpoint: the participant host:port dialed and
// the act-as role whose JWT authenticates the call. Both CLI text/JSON
// and Web UI payloads echo these fields so operators can see which
// participant a command targeted.
type resolvedLedger struct {
	Endpoint string
	Role     string
}

// resolveEndpoint fills a token verb's ledger endpoint from the instance
// when the user didn't pass --endpoint — matching the Web UI, which never
// asks for an endpoint: it resolves the instance's captured participant
// port itself (liveLedgerEndpoint). An explicit --endpoint always wins.
//
// Empty role defaults to token.DefaultRole (app-provider). On success the
// resolved host:port and role are announced on stderr so every verb that
// auto-resolves makes the endpoint contract visible; JSON/UI payloads
// also carry the same metadata via the returned struct.
//
// When the port can't be resolved (never captured), it prints the same
// diagnosis the Web UI's 503 gives (instance, role, restart, --endpoint)
// and returns errSilent so cobra exits non-zero without dumping usage.
func resolveEndpoint(cmd *cobra.Command, instance, role, endpoint string) (resolvedLedger, error) {
	if role == "" {
		role = token.DefaultRole
	}
	ep := bestEffortEndpoint(instance, role, endpoint)
	if ep == "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"no captured ledger port for instance %q role %q — restart it so ports are recorded, or pass --endpoint host:port\n",
			instance, role)
		return resolvedLedger{}, errSilent
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "using %s as %s\n", ep, role)
	return resolvedLedger{Endpoint: ep, Role: role}, nil
}
