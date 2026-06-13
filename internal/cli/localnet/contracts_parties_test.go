package localnet

import (
	"context"
	"io"
	"net"
	"testing"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
)

// fakeUserMgmt serves ListUserRights so a real ledger.Client's
// ResolveActAndReadParties resolves to a party set. Drives the
// resolveDefaultParties branches without a live participant.
type fakeUserMgmt struct {
	adminv2.UnimplementedUserManagementServiceServer
	parties []string
}

func (f *fakeUserMgmt) ListUserRights(_ context.Context, _ *adminv2.ListUserRightsRequest) (*adminv2.ListUserRightsResponse, error) {
	rights := make([]*adminv2.Right, 0, len(f.parties))
	for _, p := range f.parties {
		rights = append(rights, &adminv2.Right{
			Kind: &adminv2.Right_CanReadAs_{CanReadAs: &adminv2.Right_CanReadAs{Party: p}},
		})
	}
	return &adminv2.ListUserRightsResponse{Rights: rights}, nil
}

// dialFakeUserMgmt boots an in-process gRPC UserManagementService on a
// loopback port and returns a connected ledger.Client.
func dialFakeUserMgmt(t *testing.T, parties []string) *ledger.Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	adminv2.RegisterUserManagementServiceServer(srv, &fakeUserMgmt{parties: parties})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := ledger.Dial(context.Background(), ledger.DialOptions{
		Endpoint:         lis.Addr().String(),
		ExtraDialOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestResolveDefaultParties covers the parity helper (finding #21):
// no --party projects through the JWT's resolved parties; an explicit
// --party (or --template) is honoured verbatim; and an empty/failed
// resolution falls back to the wildcard so admin tokens still work.
func TestResolveDefaultParties(t *testing.T) {
	t.Run("no party → resolved JWT parties", func(t *testing.T) {
		client := dialFakeUserMgmt(t, []string{"alice", "bob"})
		got, err := resolveDefaultParties(context.Background(), io.Discard, client, nil, nil)
		if err != nil {
			t.Fatalf("resolveDefaultParties: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %v, want [alice bob]", got)
		}
	})

	t.Run("explicit --party is honoured verbatim (no resolution)", func(t *testing.T) {
		client := dialFakeUserMgmt(t, []string{"alice", "bob"})
		got, err := resolveDefaultParties(context.Background(), io.Discard, client, []string{"carol"}, nil)
		if err != nil {
			t.Fatalf("resolveDefaultParties: %v", err)
		}
		if len(got) != 1 || got[0] != "carol" {
			t.Errorf("got %v, want [carol]", got)
		}
	})

	t.Run("--template present → wildcard kept (parties unchanged)", func(t *testing.T) {
		client := dialFakeUserMgmt(t, []string{"alice"})
		got, err := resolveDefaultParties(context.Background(), io.Discard, client, nil, []string{"M:E"})
		if err != nil {
			t.Fatalf("resolveDefaultParties: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want [] (wildcard with template filter)", got)
		}
	})

	t.Run("no resolved parties → wildcard fallback (warns, no error)", func(t *testing.T) {
		client := dialFakeUserMgmt(t, nil) // JWT has no party rights
		got, err := resolveDefaultParties(context.Background(), io.Discard, client, nil, nil)
		if err != nil {
			t.Fatalf("resolveDefaultParties should not fail on empty rights: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want [] (wildcard fallback)", got)
		}
	})
}
