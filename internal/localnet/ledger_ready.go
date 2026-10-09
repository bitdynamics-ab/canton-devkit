package localnet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// ledgerReadyTimeout caps how long we poll the participant after Docker
// reports healthy. Canton can still be finishing ledger API bind for a
// short window after container healthchecks flip green.
// Package vars (not consts) so unit tests can shrink the budget.
var ledgerReadyTimeout = 5 * time.Minute

var ledgerReadyPollWait = 2 * time.Second

// LedgerReadyResult is what a successful readiness probe observed.
type LedgerReadyResult struct {
	Endpoint         string
	LedgerAPIVersion string
	Offset           int64
}

// ledgerProbeOptions configures a single dial attempt against a participant.
type ledgerProbeOptions struct {
	Endpoint string
	Token    string
}

// probeLedgerFn is the dial seam. Production uses probeLedger (dazl-client
// via internal/canton/ledger). Unit tests replace it to avoid a live
// participant. go-daml cannot be used here: its generated protos collide
// with dazl-client in the same process (protobuf registry panic).
var probeLedgerFn = probeLedger

// ensureLedgerReadyFn is the seam up/start/restart call after Docker
// WaitForHealthy succeeds. Tests replace it with a no-op so hermetic
// bring-up stubs do not need a reachable Ledger API.
var ensureLedgerReadyFn = ensureLedgerReady

// ensureLedgerReady resolves the app-provider Ledger API endpoint and a
// bearer JWT, then polls GetLedgerApiVersion + GetLedgerEnd until success
// or ctx expires. Role defaults to app-provider (operator node).
//
// ports should already include participant_ledger_app-provider from
// CaptureCantonPorts. creds may be empty — when so, a JWT is signed from
// projectDir's auth env files.
func ensureLedgerReady(
	ctx context.Context,
	projectDir string,
	ports map[string]int,
	creds map[string]registry.Credential,
) (LedgerReadyResult, error) {
	endpoint, err := ledgerEndpointFromPorts(ports, string(splice.RoleAppProvider))
	if err != nil {
		return LedgerReadyResult{}, err
	}
	token, err := ledgerTokenForRole(projectDir, creds, splice.RoleAppProvider)
	if err != nil {
		return LedgerReadyResult{}, err
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, ledgerReadyTimeout)
	defer cancel()

	var lastErr error
	for {
		res, err := probeLedgerFn(deadlineCtx, ledgerProbeOptions{
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
			return LedgerReadyResult{}, fmt.Errorf(
				"ledger API at %s (role %s) did not become ready: %w",
				endpoint, splice.RoleAppProvider, lastErr)
		case <-time.After(ledgerReadyPollWait):
		}
	}
}

// probeLedger dials the participant with dazl-client and runs the two
// cheap unaries that prove connectivity + auth.
func probeLedger(ctx context.Context, opts ledgerProbeOptions) (LedgerReadyResult, error) {
	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		return LedgerReadyResult{}, fmt.Errorf("ledger probe: endpoint is required")
	}

	client, err := ledger.Dial(ctx, ledger.DialOptions{
		Endpoint:  endpoint,
		Token:     ledger.StaticToken(opts.Token),
		PlainText: true,
	})
	if err != nil {
		return LedgerReadyResult{}, fmt.Errorf("ledger probe: dial %s: %w", endpoint, err)
	}
	defer func() { _ = client.Close() }()

	ver, err := client.LedgerApiVersion(ctx)
	if err != nil {
		return LedgerReadyResult{}, fmt.Errorf("ledger probe: GetLedgerApiVersion %s: %w", endpoint, err)
	}

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return LedgerReadyResult{}, fmt.Errorf("ledger probe: GetLedgerEnd %s: %w", endpoint, err)
	}

	version := ""
	if ver != nil {
		version = ver.GetVersion()
	}

	return LedgerReadyResult{
		Endpoint:         endpoint,
		LedgerAPIVersion: version,
		Offset:           end.Offset,
	}, nil
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
