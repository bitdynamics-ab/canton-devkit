package ledger

import (
	"context"
	"net"
	"testing"
	"time"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeUnaryServer is the "fan everything in" fake for the unary RPCs.
// Each test reaches in and sets the field the RPC under test reads.
//
// Embedding the Unimplemented*Server types satisfies the gRPC server
// registrations without having to implement RPCs unrelated to the test
// (they return codes.Unimplemented if anyone reaches them).
type fakeUnaryServer struct {
	lapiv2.UnimplementedEventQueryServiceServer
	lapiv2.UnimplementedPackageServiceServer
	lapiv2.UnimplementedCommandSubmissionServiceServer
	lapiv2.UnimplementedCommandServiceServer
	lapiv2.UnimplementedVersionServiceServer
	adminv2.UnimplementedPartyManagementServiceServer
	adminv2.UnimplementedPackageManagementServiceServer

	// Outputs.
	packagesList     *lapiv2.ListPackagesResponse
	apiVersion       *lapiv2.GetLedgerApiVersionResponse
	knownParties     *adminv2.ListKnownPartiesResponse
	participantId    *adminv2.GetParticipantIdResponse
	submitResp       *lapiv2.SubmitResponse
	eventsByContract *lapiv2.GetEventsByContractIdResponse
	getPackage       *lapiv2.GetPackageResponse
}

func (f *fakeUnaryServer) ListPackages(context.Context, *lapiv2.ListPackagesRequest) (*lapiv2.ListPackagesResponse, error) {
	return f.packagesList, nil
}
func (f *fakeUnaryServer) GetPackage(context.Context, *lapiv2.GetPackageRequest) (*lapiv2.GetPackageResponse, error) {
	return f.getPackage, nil
}
func (f *fakeUnaryServer) GetEventsByContractId(context.Context, *lapiv2.GetEventsByContractIdRequest) (*lapiv2.GetEventsByContractIdResponse, error) {
	return f.eventsByContract, nil
}
func (f *fakeUnaryServer) Submit(context.Context, *lapiv2.SubmitRequest) (*lapiv2.SubmitResponse, error) {
	return f.submitResp, nil
}
func (f *fakeUnaryServer) GetLedgerApiVersion(context.Context, *lapiv2.GetLedgerApiVersionRequest) (*lapiv2.GetLedgerApiVersionResponse, error) {
	return f.apiVersion, nil
}
func (f *fakeUnaryServer) ListKnownParties(context.Context, *adminv2.ListKnownPartiesRequest) (*adminv2.ListKnownPartiesResponse, error) {
	return f.knownParties, nil
}
func (f *fakeUnaryServer) GetParticipantId(context.Context, *adminv2.GetParticipantIdRequest) (*adminv2.GetParticipantIdResponse, error) {
	return f.participantId, nil
}

// newUnaryTestClient wires a Client to a bufconn server with all the
// unary fakes registered. Returns the Client + fake + teardown.
func newUnaryTestClient(t *testing.T) (*Client, *fakeUnaryServer, func()) {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fake := &fakeUnaryServer{}
	lapiv2.RegisterEventQueryServiceServer(srv, fake)
	lapiv2.RegisterPackageServiceServer(srv, fake)
	lapiv2.RegisterCommandSubmissionServiceServer(srv, fake)
	lapiv2.RegisterCommandServiceServer(srv, fake)
	lapiv2.RegisterVersionServiceServer(srv, fake)
	adminv2.RegisterPartyManagementServiceServer(srv, fake)
	adminv2.RegisterPackageManagementServiceServer(srv, fake)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	dialer := func(_ context.Context, _ string) (net.Conn, error) { return lis.Dial() }
	client, err := Dial(context.Background(), DialOptions{
		Endpoint: "passthrough:///bufnet",
		ExtraDialOptions: []grpc.DialOption{
			grpc.WithContextDialer(dialer),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		_ = lis.Close()
		srv.Stop()
		t.Fatalf("Dial: %v", err)
	}
	teardown := func() {
		_ = client.Close()
		srv.Stop()
		_ = lis.Close()
		select {
		case <-serveErr:
		case <-time.After(time.Second):
		}
	}
	return client, fake, teardown
}

// TestUnaries_RoundTrip is the lock-in for every unary's wiring in one
// table-driven test. Each row constructs a canned response, invokes the
// corresponding Client method, and asserts the round-trip preserves
// identity (or a single signal field). This catches:
//   - missing client-stub wiring in client.go (a forgotten New*ServiceClient
//     constructor would yield a nil pointer dereference here).
//   - typo'd method names in wrapper files.
//   - wrong service stub used (e.g. ListKnownParties wired to packageMgmt
//     by mistake).
//
// Per-method semantic tests live in individual ..._test.go files if and
// when a method grows non-trivial behaviour; today every unary is a
// pass-through, so the table is the right level of coverage.
func TestUnaries_RoundTrip(t *testing.T) {
	client, fake, teardown := newUnaryTestClient(t)
	defer teardown()

	// Seed all outputs the table will exercise.
	fake.packagesList = &lapiv2.ListPackagesResponse{PackageIds: []string{"pkg-a", "pkg-b"}}
	fake.getPackage = &lapiv2.GetPackageResponse{HashFunction: 1}
	fake.eventsByContract = &lapiv2.GetEventsByContractIdResponse{} // empty is fine, just probe round-trip
	fake.submitResp = &lapiv2.SubmitResponse{}
	fake.apiVersion = &lapiv2.GetLedgerApiVersionResponse{Version: "v2.3.0-test"}
	fake.knownParties = &adminv2.ListKnownPartiesResponse{
		PartyDetails: []*adminv2.PartyDetails{{Party: "Alice"}, {Party: "Bob"}},
	}
	fake.participantId = &adminv2.GetParticipantIdResponse{ParticipantId: "participant::alice"}

	cases := []struct {
		name string
		run  func() error
	}{
		{"ListPackages", func() error {
			resp, err := client.ListPackages(context.Background())
			if err != nil {
				return err
			}
			if len(resp.GetPackageIds()) != 2 {
				t.Errorf("ListPackages: got %d ids, want 2", len(resp.GetPackageIds()))
			}
			return nil
		}},
		{"GetPackage", func() error {
			resp, err := client.GetPackage(context.Background(), "pkg-a")
			if err != nil {
				return err
			}
			if resp.GetHashFunction() != 1 {
				t.Errorf("GetPackage: got hash %v, want 1", resp.GetHashFunction())
			}
			return nil
		}},
		{"EventsByContractId", func() error {
			_, err := client.EventsByContractId(context.Background(), &lapiv2.GetEventsByContractIdRequest{ContractId: "cid-1"})
			return err
		}},
		{"Submit", func() error {
			_, err := client.Submit(context.Background(), &lapiv2.SubmitRequest{})
			return err
		}},
		{"LedgerApiVersion", func() error {
			resp, err := client.LedgerApiVersion(context.Background())
			if err != nil {
				return err
			}
			if resp.GetVersion() != "v2.3.0-test" {
				t.Errorf("LedgerApiVersion: got %q, want v2.3.0-test", resp.GetVersion())
			}
			return nil
		}},
		{"ListKnownParties", func() error {
			resp, err := client.ListKnownParties(context.Background())
			if err != nil {
				return err
			}
			if len(resp.GetPartyDetails()) != 2 {
				t.Errorf("ListKnownParties: got %d parties, want 2", len(resp.GetPartyDetails()))
			}
			return nil
		}},
		{"GetParticipantId", func() error {
			resp, err := client.GetParticipantId(context.Background())
			if err != nil {
				return err
			}
			if resp.GetParticipantId() != "participant::alice" {
				t.Errorf("GetParticipantId: got %q", resp.GetParticipantId())
			}
			return nil
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		})
	}
}
