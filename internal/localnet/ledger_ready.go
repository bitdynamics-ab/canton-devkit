package localnet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/poc/godamlprobe"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// ledgerReadyTimeout caps how long we poll the participant after Docker
// reports healthy. Canton can still be finishing ledger API bind for a
// short window after container healthchecks flip green.
// Package vars (not consts) so unit tests can shrink the budget.
var ledgerReadyTimeout = 5 * time.Minute

var ledgerReadyPollWait = 2 * time.Second

// probeLedgerFn is the go-daml dial seam. Production uses
// godamlprobe.Probe; unit tests replace it to avoid a live participant.
var probeLedgerFn = godamlprobe.Probe

// ensureLedgerReadyFn is the seam up/start/restart call after Docker
// WaitForHealthy succeeds. Tests replace it with a no-op so hermetic
// bring-up stubs do not need a reachable Ledger API.
var ensureLedgerReadyFn = ensureLedgerReady

// ensureLedgerReady resolves the app-provider Ledger API endpoint and a
// bearer JWT, then polls go-daml GetLedgerApiVersion + GetLedgerEnd until
// success or ctx expires. Role defaults to app-provider (operator node).
//
// ports should already include participant_ledger_app-provider from
// CaptureCantonPorts. creds may be empty — when so, a JWT is signed from
// projectDir's auth env files.
func ensureLedgerReady(
	ctx context.Context,
	projectDir string,
	ports map[string]int,
	creds map[string]registry.Credential,
) (godamlprobe.Result, error) {
	endpoint, err := ledgerEndpointFromPorts(ports, string(splice.RoleAppProvider))
	if err != nil {
		return godamlprobe.Result{}, err
	}
	token, err := ledgerTokenForRole(projectDir, creds, splice.RoleAppProvider)
	if err != nil {
		return godamlprobe.Result{}, err
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, ledgerReadyTimeout)
	defer cancel()

	var lastErr error
	for {
		res, err := probeLedgerFn(deadlineCtx, godamlprobe.Options{
			Endpoint: endpoint,
			Token:    token,
		})
		if err == nil {
			return res, nil
		}
		lastErr = err
		select {
		case <-deadlineCtx.Done():
			if lastErr == nil {
				lastErr = deadlineCtx.Err()
			}
			return godamlprobe.Result{}, fmt.Errorf(
				"ledger API at %s (role %s) did not become ready: %w",
				endpoint, splice.RoleAppProvider, lastErr)
		case <-time.After(ledgerReadyPollWait):
		}
	}
}

func ledgerEndpointFromPorts(ports map[string]int, role string) (string, error) {
	if role == "" {
		role = string(splice.RoleAppProvider)
	}
	key := "participant_ledger_" + role
	port, ok := ports[key]
	if !ok || port <= 0 {
		return "", fmt.Errorf(
			"ledger endpoint for role %s not captured (missing %s in instance ports)",
			role, key)
	}
	return "localhost:" + strconv.Itoa(port), nil
}

func ledgerTokenForRole(
	projectDir string,
	creds map[string]registry.Credential,
	role splice.Role,
) (string, error) {
	if c, ok := creds[string(role)]; ok && strings.TrimSpace(c.JWT) != "" {
		return c.JWT, nil
	}
	if projectDir == "" {
		return "", fmt.Errorf("no JWT for role %s and projectDir is empty", role)
	}
	inputs, err := splice.LoadCredentialInputs(projectDir)
	if err != nil {
		return "", fmt.Errorf("load credential inputs for ledger probe: %w", err)
	}
	for _, in := range inputs {
		if in.Role != role {
			continue
		}
		tok, err := splice.SignToken(in)
		if err != nil {
			return "", fmt.Errorf("sign JWT for role %s: %w", role, err)
		}
		return tok, nil
	}
	return "", fmt.Errorf("no auth env inputs for role %s in %s", role, projectDir)
}
