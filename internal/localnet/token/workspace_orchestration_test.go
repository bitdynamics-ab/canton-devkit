package token

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestScanWorkspace_NoReadablePartiesReturnsEmpty pins the no-parties
// branch in scanWorkspace: when neither ResolveActAndReadParties nor
// the localPartiesForRole fallback yields a party (a fresh JWT before
// any grant lands, or a role with no local hosts), scanWorkspace
// returns an empty *Workspace rather than failing the ACS query. This
// was the no-parties skip note on PR #89's review.
func TestScanWorkspace_NoReadablePartiesReturnsEmpty(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	fake := &fakeLedger{
		// Defaults: ResolveActAndReadParties → nil, ListKnownParties → empty.
		// ActiveContracts must NOT be reached.
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			t.Fatal("ActiveContracts should not be reached when no parties resolve")
			return nil, nil
		},
	}
	withFakeDial(t, fake)

	ws, err := scanWorkspace(context.Background(), liveOpts("demo", "app-user"))
	if err != nil {
		t.Fatalf("scanWorkspace: %v (want nil — empty workspace, not error)", err)
	}
	if ws == nil {
		t.Fatal("scanWorkspace returned nil workspace; want empty &Workspace{}")
	}
	if len(ws.Holdings) != 0 || len(ws.Parties) != 0 {
		t.Errorf("workspace not empty: %+v", ws)
	}
}

// TestScanWorkspace_StreamErrorIsWrapped pins the stream-error branch:
// a mid-stream gRPC error from ActiveContracts reaches the caller
// wrapped under "ACS stream:" so the HTTP handler's mapTokenError can
// still extract the gRPC code via errors.Unwrap + status.FromError.
func TestScanWorkspace_StreamErrorIsWrapped(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	streamErr := status.Error(codes.Unavailable, "participant restarting")
	fake := &fakeLedger{
		ResolveActAndReadPartiesFn: func(ctx context.Context) ([]string, error) {
			return []string{"app_user_alice"}, nil
		},
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream(nil, streamErr), nil
		},
	}
	withFakeDial(t, fake)

	_, err := scanWorkspace(context.Background(), liveOpts("demo", "app-user"))
	if err == nil {
		t.Fatal("scanWorkspace: got nil error; want stream error")
	}
	if !strings.Contains(err.Error(), "ACS stream:") {
		t.Errorf("error not wrapped under 'ACS stream:': %v", err)
	}
	if st, ok := status.FromError(errors.Unwrap(err)); !ok || st.Code() != codes.Unavailable {
		t.Errorf("wrapped error lost Unavailable code: %v", err)
	}
}

// TestScanWorkspace_PermissionDeniedOnResolveBubbles pins the
// "PermissionDenied widens" intent from PR #89's review: when
// ResolveActAndReadParties fails with PermissionDenied (the role's
// JWT was rejected outright), the orchestration surfaces the
// wrapped error so the handler can either retry with a higher-
// privilege role or map it to 403. A best-effort grant failure in
// resolveReadableParties does NOT block the scan — only the resolve
// itself does — which is what this test isolates.
func TestScanWorkspace_PermissionDeniedOnResolveBubbles(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	denied := status.Error(codes.PermissionDenied, "JWT rejected")
	fake := &fakeLedger{
		ResolveActAndReadPartiesFn: func(ctx context.Context) ([]string, error) {
			return nil, denied
		},
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			t.Fatal("ActiveContracts should not be reached after resolve failure")
			return nil, nil
		},
	}
	withFakeDial(t, fake)

	_, err := scanWorkspace(context.Background(), liveOpts("demo", "app-user"))
	if err == nil {
		t.Fatal("scanWorkspace: got nil error; want PermissionDenied")
	}
	if !strings.Contains(err.Error(), "resolve readable parties") {
		t.Errorf("error not wrapped under 'resolve readable parties': %v", err)
	}
	if st, ok := status.FromError(errors.Unwrap(err)); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("wrapped error lost PermissionDenied: %v", err)
	}
}

// TestScanWorkspace_BestEffortGrantFailureDoesNotBlockScan pins the
// "grant is best-effort" contract: if GrantUserActAndReadAs fails
// (e.g. the JWT lacks the user-management right), resolveReadableParties
// swallows the grant error and still attempts the resolve. So the
// scan continues with the parties the JWT can already read — degraded
// god-mode rather than a hard failure.
func TestScanWorkspace_BestEffortGrantFailureDoesNotBlockScan(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	fake := &fakeLedger{
		GrantUserActAndReadAsFn: func(ctx context.Context, _ string, _ []string) error {
			return status.Error(codes.PermissionDenied, "no user-management right")
		},
		ResolveActAndReadPartiesFn: func(ctx context.Context) ([]string, error) {
			return []string{"app_user_alice"}, nil
		},
		// Empty ACS — fake ActiveContracts default returns an empty closed stream.
	}
	withFakeDial(t, fake)

	ws, err := scanWorkspace(context.Background(), liveOpts("demo", "app-user"))
	if err != nil {
		t.Fatalf("scanWorkspace: %v (best-effort grant failure should not propagate)", err)
	}
	if ws == nil {
		t.Fatal("scanWorkspace returned nil workspace")
	}
}

// TestScanWorkspace_TruncatesAtCap pins the cap on scanWorkspace — the
// same maxWorkspaceScan guard runBalanceLive uses, asserted here so a
// regression that drops the break can't slip past the workspace path.
func TestScanWorkspace_TruncatesAtCap(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	items := make([]*lapiv2.GetActiveContractsResponse, maxWorkspaceScan+1)
	for i := range items {
		items[i] = emptyActiveContract()
	}
	fake := &fakeLedger{
		ResolveActAndReadPartiesFn: func(ctx context.Context) ([]string, error) {
			return []string{"app_user_alice"}, nil
		},
		ListKnownPartiesFn: func(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
			return &adminv2.ListKnownPartiesResponse{
				PartyDetails: []*adminv2.PartyDetails{{Party: "app_user_alice", IsLocal: true}},
			}, nil
		},
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream(items, nil), nil
		},
	}
	withFakeDial(t, fake)

	ws, err := scanWorkspace(context.Background(), liveOpts("demo", "app-user"))
	if err != nil {
		t.Fatalf("scanWorkspace: %v", err)
	}
	if !ws.Truncated {
		t.Errorf("Truncated = false; want true after %d items (cap %d)",
			len(items), maxWorkspaceScan)
	}
}
