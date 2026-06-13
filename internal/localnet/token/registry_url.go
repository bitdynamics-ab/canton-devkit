package token

import (
	"fmt"
	"strconv"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// LocalNet scan-registry routing. The V2 Token Standard's transfer
// registry endpoints (`/registry/transfer-instruction/v2/...`) are
// served by the Splice scan app (`splice:5012` inside the docker
// network), which the LocalNet nginx exposes behind the SV UI port
// under the `scan.localhost` virtual host:
//
//	cluster/compose/localnet/conf/nginx/sv.conf
//	  server { listen ${SV_UI_PORT}; server_name scan.localhost;
//	    location /registry { proxy_pass http://splice:5012/registry; } }
//
// So from the host, the registry base URL is the SV UI port with a
// `Host: scan.localhost` header — derived here from the registry
// state's recorded ports.

// scanVHost is the nginx virtual-host name the scan registry routes
// live under on LocalNet. Real DevNet deployments give scan its own
// DNS name, so this override is LocalNet-specific.
const scanVHost = "scan.localhost"

// resolveRegistryURL returns the (baseURL, hostHeader) the registry
// client needs to reach the scan app's V2 transfer endpoints for the
// given instance. Returns an error when the SV UI port wasn't
// recorded (instance pre-dates port capture, or came up without the
// SV profile).
//
// An explicit override (CLI --registry-url) short-circuits this —
// callers pass override != "" to use a real DevNet scan URL instead
// of the LocalNet nginx vhost trick. In that case hostHeader is empty
// (the override URL carries its own authority).
func resolveRegistryURL(instance, override string) (baseURL, hostHeader string, err error) {
	if override != "" {
		return override, "", nil
	}
	state, err := registry.Read(instance)
	if err != nil {
		return "", "", fmt.Errorf("read instance state: %w", err)
	}
	port, ok := state.Ports["sv_ui"]
	if !ok || port == 0 {
		return "", "", fmt.Errorf(
			"instance %q has no recorded sv_ui port — the V2 transfer "+
				"registry is served behind the SV UI nginx vhost; restart "+
				"the instance so ports are captured, or pass --registry-url "+
				"with the scan app's URL explicitly", instance)
	}
	return "http://localhost:" + strconv.Itoa(port), scanVHost, nil
}

// resolveRegistryToken returns the bearer JWT the scan registry
// accepts for `role` on `instance`. The scan app validates the same
// `unsafe`-signed per-role tokens the ledger API takes, so we reuse
// the ledger token-resolution chain (captured creds → SignToken
// fallback). An explicit token short-circuits. Returns "" on any
// resolution failure — the registry call then proceeds unauthenticated
// and the scan app surfaces a 401 the caller can act on, which is a
// clearer signal than failing here before the request is even made.
func resolveRegistryToken(explicit, instance, role string) string {
	if explicit != "" {
		return explicit
	}
	tok, err := resolveLedgerToken(LedgerConn{Instance: instance, Role: role})
	if err != nil {
		return ""
	}
	return tok
}
