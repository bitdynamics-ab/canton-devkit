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

// liveOpts is a BalanceOptions with Endpoint set so RunBalance / the
// orchestration path takes the live-ACS branch rather than the
// registry-derived pseudo-balance path.
func liveOpts(instance, role string) BalanceOptions {
	return BalanceOptions{
		Instance: instance,
		Role:     role,
		Endpoint: "fake:0", // non-empty triggers runBalanceLive
		Insecure: true,
	}
}

// TestRunBalanceLive_NoPartiesReturnsEmpty pins the no-parties branch:
// when --party is unset and localPartiesForRole returns nothing (the
// JWT can't read any local party), runBalanceLive returns (nil, nil)
// rather than failing the ACS query with an empty FiltersByParty
// (which Canton rejects with INVALID_ARGUMENT). The handler then
// surfaces an empty balance, not a 500. This was the no-parties skip
// note on PR #86's review.
func TestRunBalanceLive_NoPartiesReturnsEmpty(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	fake := &fakeLedger{
		// Empty PartyDetails → localPartiesForRole finds nothing.
		ListKnownPartiesFn: func(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
			return &adminv2.ListKnownPartiesResponse{}, nil
		},
		// ActiveContracts MUST NOT be called on this path — fail if it is.
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			t.Fatal("ActiveContracts should not be reached when no parties resolve")
			return nil, nil
		},
	}
	withFakeDial(t, fake)

	rows, truncated, err := runBalanceLive(context.Background(), liveOpts("demo", "app-user"))
	if err != nil {
		t.Fatalf("runBalanceLive: %v (want nil — no parties is not an error)", err)
	}
	if rows != nil {
		t.Errorf("rows = %v; want nil", rows)
	}
	if truncated {
		t.Errorf("truncated = true; want false on empty path")
	}
}

// TestRunBalanceLive_StreamErrorIsWrapped pins the stream-error branch:
// a mid-stream gRPC error reaches the caller wrapped under "ACS stream:"
// so handlers' mapTokenError can still extract the gRPC code (the
// wrapper preserves status.FromError via errors.Is/Unwrap).
func TestRunBalanceLive_StreamErrorIsWrapped(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	streamErr := status.Error(codes.PermissionDenied, "no readAs grant for party x")
	fake := &fakeLedger{
		ListKnownPartiesFn: func(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
			return &adminv2.ListKnownPartiesResponse{
				PartyDetails: []*adminv2.PartyDetails{
					{Party: "app_user_alice", IsLocal: true},
				},
			}, nil
		},
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream(nil, streamErr), nil
		},
	}
	withFakeDial(t, fake)

	_, _, err := runBalanceLive(context.Background(), liveOpts("demo", "app-user"))
	if err == nil {
		t.Fatal("runBalanceLive: got nil error; want stream error")
	}
	if !strings.Contains(err.Error(), "ACS stream:") {
		t.Errorf("error not wrapped under 'ACS stream:': %v", err)
	}
	// The gRPC code must survive unwrap so mapTokenError can route it
	// to 403 rather than 500.
	if st, ok := status.FromError(errors.Unwrap(err)); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("wrapped error lost PermissionDenied code: %v", err)
	}
}

// TestRunBalanceLive_PermissionDeniedOnLocalParties pins the
// "PermissionDenied widens" intent from PR #86's review: when the
// per-role JWT can't enumerate parties (ListKnownParties returns
// PermissionDenied), the orchestration surfaces a wrapped error
// rather than silently masking it as an empty balance — so the
// handler caller can widen to a higher-privilege role on retry.
func TestRunBalanceLive_PermissionDeniedOnLocalParties(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	denied := status.Error(codes.PermissionDenied, "user lacks ParticipantAdmin")
	fake := &fakeLedger{
		ListKnownPartiesFn: func(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
			return nil, denied
		},
	}
	withFakeDial(t, fake)

	_, _, err := runBalanceLive(context.Background(), liveOpts("demo", "app-user"))
	if err == nil {
		t.Fatal("runBalanceLive: got nil error; want PermissionDenied")
	}
	if !strings.Contains(err.Error(), "discover local parties") {
		t.Errorf("error not wrapped under 'discover local parties': %v", err)
	}
	if st, ok := status.FromError(errors.Unwrap(err)); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("wrapped error lost PermissionDenied: %v", err)
	}
}

// TestRunBalanceLive_TruncatesAtCap pins B2 from PR #86's review: a
// runaway ACS stream (10_001 items) is capped at maxWorkspaceScan and
// the truncated flag flows back so the public RunBalance wrapper can
// surface a partial-result warning. The empty ContractEntry shape
// decodes cleanly past the type-assertion but yields no
// InterfaceViews, so we're testing the cap itself, not view parsing.
func TestRunBalanceLive_TruncatesAtCap(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	items := make([]*lapiv2.GetActiveContractsResponse, maxWorkspaceScan+1)
	for i := range items {
		items[i] = emptyActiveContract()
	}
	fake := &fakeLedger{
		ListKnownPartiesFn: func(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
			return &adminv2.ListKnownPartiesResponse{
				PartyDetails: []*adminv2.PartyDetails{
					{Party: "app_user_alice", IsLocal: true},
				},
			}, nil
		},
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream(items, nil), nil
		},
	}
	withFakeDial(t, fake)

	_, truncated, err := runBalanceLive(context.Background(), liveOpts("demo", "app-user"))
	if err != nil {
		t.Fatalf("runBalanceLive: %v", err)
	}
	if !truncated {
		t.Errorf("truncated = false; want true after streaming %d items (cap %d)",
			len(items), maxWorkspaceScan)
	}
}
