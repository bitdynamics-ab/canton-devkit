package admin

import (
	"context"
	"errors"
	"net"
	"testing"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// stubPackageService is an in-memory PackageServiceServer used by every
// test in this file. Each method returns canned responses and records
// the requests it saw; tests inspect the recordings to assert the right
// request fields were wired.
//
// Only the methods the CLI dar/package commands exercise are
// implemented. The embedded UnimplementedPackageServiceServer makes the
// others return codes.Unimplemented — exactly what should happen if a
// command starts calling a method it shouldn't.
type stubPackageService struct {
	adminproto.UnimplementedPackageServiceServer

	uploadReqs   []*adminproto.UploadDarRequest
	validateReqs []*adminproto.ValidateDarRequest
	listDarsReqs []*adminproto.ListDarsRequest
	listPkgsReqs []*adminproto.ListPackagesRequest
	getDarReqs   []*adminproto.GetDarRequest
	removeReqs   []*adminproto.RemoveDarRequest
	unvetReqs    []*adminproto.UnvetDarRequest

	uploadResp   *adminproto.UploadDarResponse
	validateResp *adminproto.ValidateDarResponse
	listDarsResp *adminproto.ListDarsResponse
	listPkgsResp *adminproto.ListPackagesResponse
	getDarResp   *adminproto.GetDarResponse
	uploadErr    error
}

func (s *stubPackageService) UploadDar(_ context.Context, req *adminproto.UploadDarRequest) (*adminproto.UploadDarResponse, error) {
	s.uploadReqs = append(s.uploadReqs, req)
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	return s.uploadResp, nil
}

func (s *stubPackageService) ValidateDar(_ context.Context, req *adminproto.ValidateDarRequest) (*adminproto.ValidateDarResponse, error) {
	s.validateReqs = append(s.validateReqs, req)
	return s.validateResp, nil
}

func (s *stubPackageService) ListDars(_ context.Context, req *adminproto.ListDarsRequest) (*adminproto.ListDarsResponse, error) {
	s.listDarsReqs = append(s.listDarsReqs, req)
	return s.listDarsResp, nil
}

func (s *stubPackageService) ListPackages(_ context.Context, req *adminproto.ListPackagesRequest) (*adminproto.ListPackagesResponse, error) {
	s.listPkgsReqs = append(s.listPkgsReqs, req)
	return s.listPkgsResp, nil
}

func (s *stubPackageService) GetDar(_ context.Context, req *adminproto.GetDarRequest) (*adminproto.GetDarResponse, error) {
	s.getDarReqs = append(s.getDarReqs, req)
	return s.getDarResp, nil
}

func (s *stubPackageService) RemoveDar(_ context.Context, req *adminproto.RemoveDarRequest) (*adminproto.RemoveDarResponse, error) {
	s.removeReqs = append(s.removeReqs, req)
	return &adminproto.RemoveDarResponse{}, nil
}

func (s *stubPackageService) UnvetDar(_ context.Context, req *adminproto.UnvetDarRequest) (*adminproto.UnvetDarResponse, error) {
	s.unvetReqs = append(s.unvetReqs, req)
	return &adminproto.UnvetDarResponse{}, nil
}

// newStubClient spins up an in-process gRPC server backed by stub and
// returns a *Client connected to it via bufconn. Caller owns calling
// client.Close() in a defer.
func newStubClient(t *testing.T, stub *stubPackageService) *Client {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	adminproto.RegisterPackageServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	dialer := func(_ context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(context.Background())
	}
	cc, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return NewWithConn(cc)
}

func TestUploadDar(t *testing.T) {
	stub := &stubPackageService{
		uploadResp: &adminproto.UploadDarResponse{DarIds: []string{"abc123"}},
	}
	client := newStubClient(t, stub)

	resp, err := client.Package.UploadDar(context.Background(), &adminproto.UploadDarRequest{
		Dars:           []*adminproto.UploadDarRequest_UploadDarData{{Bytes: []byte("dar-bytes")}},
		VetAllPackages: true,
	})
	if err != nil {
		t.Fatalf("UploadDar: %v", err)
	}
	if len(resp.GetDarIds()) != 1 || resp.GetDarIds()[0] != "abc123" {
		t.Errorf("unexpected DarIds: %v", resp.GetDarIds())
	}
	if len(stub.uploadReqs) != 1 {
		t.Fatalf("server saw %d upload calls, want 1", len(stub.uploadReqs))
	}
	got := stub.uploadReqs[0]
	if string(got.GetDars()[0].GetBytes()) != "dar-bytes" {
		t.Errorf("dar payload not forwarded")
	}
	if !got.GetVetAllPackages() {
		t.Errorf("VetAllPackages flag not forwarded")
	}
}

func TestUploadDar_ServerError(t *testing.T) {
	stub := &stubPackageService{uploadErr: errors.New("simulated rejection")}
	client := newStubClient(t, stub)
	_, err := client.Package.UploadDar(context.Background(), &adminproto.UploadDarRequest{})
	if err == nil {
		t.Fatal("expected error from server, got nil")
	}
}

func TestValidateDar(t *testing.T) {
	stub := &stubPackageService{
		validateResp: &adminproto.ValidateDarResponse{MainPackageId: "main-id"},
	}
	client := newStubClient(t, stub)

	resp, err := client.Package.ValidateDar(context.Background(), &adminproto.ValidateDarRequest{
		Data:     []byte("dar"),
		Filename: "foo.dar",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetMainPackageId() != "main-id" {
		t.Errorf("MainPackageId = %q", resp.GetMainPackageId())
	}
	if stub.validateReqs[0].GetFilename() != "foo.dar" {
		t.Errorf("filename not forwarded")
	}
}

func TestListDars(t *testing.T) {
	stub := &stubPackageService{
		listDarsResp: &adminproto.ListDarsResponse{
			Dars: []*adminproto.DarDescription{
				{Main: "id1", Name: "foo", Version: "1.0.0"},
				{Main: "id2", Name: "bar", Version: "2.0.0"},
			},
		},
	}
	client := newStubClient(t, stub)

	resp, err := client.Package.ListDars(context.Background(), &adminproto.ListDarsRequest{
		Limit:      50,
		FilterName: "foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetDars()) != 2 {
		t.Errorf("want 2 dars, got %d", len(resp.GetDars()))
	}
	if stub.listDarsReqs[0].GetLimit() != 50 || stub.listDarsReqs[0].GetFilterName() != "foo" {
		t.Errorf("list flags not forwarded: %+v", stub.listDarsReqs[0])
	}
}

func TestListPackages(t *testing.T) {
	now := timestamppb.Now()
	stub := &stubPackageService{
		listPkgsResp: &adminproto.ListPackagesResponse{
			PackageDescriptions: []*adminproto.PackageDescription{
				{PackageId: "id1", Name: "daml-prim", Version: "0.0.0", UploadedAt: now, Size: 12345},
			},
		},
	}
	client := newStubClient(t, stub)

	resp, err := client.Package.ListPackages(context.Background(), &adminproto.ListPackagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetPackageDescriptions()) != 1 || resp.GetPackageDescriptions()[0].GetSize() != 12345 {
		t.Errorf("package fields not preserved: %+v", resp.GetPackageDescriptions())
	}
}

func TestGetDar(t *testing.T) {
	stub := &stubPackageService{
		getDarResp: &adminproto.GetDarResponse{
			Payload: []byte("dar-bytes-here"),
			Data:    &adminproto.DarDescription{Main: "id1", Name: "foo", Version: "1.0.0"},
		},
	}
	client := newStubClient(t, stub)

	resp, err := client.Package.GetDar(context.Background(), &adminproto.GetDarRequest{MainPackageId: "id1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.GetPayload()) != "dar-bytes-here" {
		t.Errorf("payload mismatch")
	}
	if resp.GetData().GetName() != "foo" {
		t.Errorf("DarDescription not forwarded")
	}
	if stub.getDarReqs[0].GetMainPackageId() != "id1" {
		t.Errorf("MainPackageId not forwarded")
	}
}

func TestRemoveDar(t *testing.T) {
	stub := &stubPackageService{}
	client := newStubClient(t, stub)

	_, err := client.Package.RemoveDar(context.Background(), &adminproto.RemoveDarRequest{MainPackageId: "id1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.removeReqs) != 1 || stub.removeReqs[0].GetMainPackageId() != "id1" {
		t.Errorf("RemoveDar request not forwarded correctly")
	}
}

func TestUnvetDar(t *testing.T) {
	stub := &stubPackageService{}
	client := newStubClient(t, stub)

	_, err := client.Package.UnvetDar(context.Background(), &adminproto.UnvetDarRequest{MainPackageId: "id1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.unvetReqs) != 1 || stub.unvetReqs[0].GetMainPackageId() != "id1" {
		t.Errorf("UnvetDar request not forwarded correctly")
	}
}

func TestBearerTokenCarriedInMetadata(t *testing.T) {
	// Verify the PerRPCCredentials wire-up sends "authorization: Bearer <token>"
	// even on insecure channels (Splice LocalNet plaintext is the common case).
	// Done at the metadata layer so a future change to grpc package won't
	// silently break the auth header.
	cfg := Config{Token: "test-token", Insecure: true}
	creds := bearerToken(cfg.Token, cfg.Insecure)
	md, err := creds.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if md["authorization"] != "Bearer test-token" {
		t.Errorf("authorization header = %q", md["authorization"])
	}
	if creds.RequireTransportSecurity() {
		t.Errorf("RequireTransportSecurity should be false when Insecure=true")
	}
}
