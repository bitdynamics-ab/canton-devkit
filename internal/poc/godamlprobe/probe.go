package godamlprobe

import (
	"context"
	"fmt"
	"strings"

	"github.com/noders-team/go-daml/pkg/auth"
	godamlclient "github.com/noders-team/go-daml/pkg/client"
	"github.com/noders-team/go-daml/pkg/model"
)

// Options configures a go-daml connectivity probe against a participant
// Ledger API gRPC endpoint.
type Options struct {
	// Endpoint is host:port, e.g. "localhost:5001". Required.
	Endpoint string

	// Token is a bearer JWT the participant accepts. Empty is allowed only
	// for participants configured without auth (rare outside tests).
	Token string
}

// Result is what a successful probe observed. Offset is the ledger end;
// LedgerAPIVersion is the participant-reported API version string.
type Result struct {
	Endpoint         string
	LedgerAPIVersion string
	Offset           int64
}

// Probe dials the participant with go-daml and runs two cheap unaries:
// GetLedgerApiVersion (go-daml's Ping) and GetLedgerEnd. Success means the
// endpoint is reachable and the token (if any) was accepted.
//
// LocalNet participants speak plaintext gRPC; go-daml defaults to insecure
// transport when no TLS config is set, which matches that layout.
func Probe(ctx context.Context, opts Options) (Result, error) {
	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		return Result{}, fmt.Errorf("godamlprobe.Probe: endpoint is required")
	}

	cl, err := godamlclient.NewDamlClient(endpoint, auth.NewBearerTokenProvider(opts.Token)).
		Build(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("godamlprobe.Probe: dial %s: %w", endpoint, err)
	}
	defer cl.Close()

	// GetLedgerAPIVersion is go-daml's Ping implementation — one unary
	// that proves dial + auth + stub wiring. GetLedgerEnd is the same
	// readiness probe internal/canton/ledger documents for dazl-client.
	ver, err := cl.VersionService.GetLedgerAPIVersion(ctx, &model.GetLedgerAPIVersionRequest{})
	if err != nil {
		return Result{}, fmt.Errorf("godamlprobe.Probe: GetLedgerAPIVersion %s: %w", endpoint, err)
	}

	end, err := cl.StateService.GetLedgerEnd(ctx, &model.GetLedgerEndRequest{})
	if err != nil {
		return Result{}, fmt.Errorf("godamlprobe.Probe: GetLedgerEnd %s: %w", endpoint, err)
	}

	version := ""
	if ver != nil {
		version = ver.Version
	}
	var offset int64
	if end != nil {
		offset = end.Offset
	}

	return Result{
		Endpoint:         endpoint,
		LedgerAPIVersion: version,
		Offset:           offset,
	}, nil
}
