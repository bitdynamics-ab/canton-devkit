package ledger

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// fakeStateServer is the minimum surface needed to exercise the first
// vertical slice: GetLedgerEnd. It records the auth header it observed so
// tests can prove the interceptor wired correctly, and lets the test
// configure the returned offset + error.
//
// All other StateServiceServer RPCs panic if called — they should not be
// reached by the tests in this file. As we add more state.go methods we
// either expand this fake or use the UnimplementedStateServiceServer
// embedding (which returns codes.Unimplemented).
type fakeStateServer struct {
	lapiv2.UnimplementedStateServiceServer

	// Outputs the test configures.
	offset int64
	err    error

	// Captured by Serve for assertion.
	lastAuth string
}

func (f *fakeStateServer) GetLedgerEnd(ctx context.Context, _ *lapiv2.GetLedgerEndRequest) (*lapiv2.GetLedgerEndResponse, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("authorization"); len(v) > 0 {
			f.lastAuth = v[0]
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &lapiv2.GetLedgerEndResponse{Offset: f.offset}, nil
}

// newTestClient wires a Client to an in-memory bufconn-backed gRPC server.
// Returns the Client, the fake state server (for inspection), and a
// teardown func that the test must defer. The fake's outputs default to
// {offset=0, err=nil}; tests mutate them in place before invoking RPCs.
func newTestClient(t *testing.T, token TokenSource) (*Client, *fakeStateServer, func()) {
	t.Helper()

	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)

	srv := grpc.NewServer()
	fake := &fakeStateServer{}
	lapiv2.RegisterStateServiceServer(srv, fake)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	dialer := func(_ context.Context, _ string) (net.Conn, error) { return lis.Dial() }

	client, err := Dial(context.Background(), DialOptions{
		// `passthrough://` scheme bypasses grpc.NewClient's default DNS
		// resolver (which would reject "bufnet" with "produced zero
		// addresses"). The bufconn dialer overrides the address anyway —
		// only the resolver indirection matters here.
		Endpoint: "passthrough:///bufnet",
		Token:    token,
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
		// Drain Serve goroutine. Serve returns nil on graceful stop or an
		// error on listener close; either is fine.
		select {
		case <-serveErr:
		case <-time.After(time.Second):
			t.Log("warning: serve goroutine did not exit within 1s")
		}
	}
	return client, fake, teardown
}

// TestDial_RejectsEmptyEndpoint pins the cheapest user-error case: a Dial
// with no endpoint fails fast with a clear message, no goroutine spawned.
func TestDial_RejectsEmptyEndpoint(t *testing.T) {
	_, err := Dial(context.Background(), DialOptions{Endpoint: ""})
	if err == nil {
		t.Fatal("expected error for empty Endpoint, got nil")
	}
	const want = "Endpoint is required"
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error = %q, want substring %q", got, want)
	}
}

// TestLedgerEnd_HappyPath is the end-to-end wiring proof: Dial through a
// bufconn-backed fake, call LedgerEnd, assert the returned offset matches
// what the fake said. If this passes, the gRPC stub plumbing is sound.
func TestLedgerEnd_HappyPath(t *testing.T) {
	client, fake, teardown := newTestClient(t, StaticToken("test-token"))
	defer teardown()

	fake.offset = 42

	got, err := client.LedgerEnd(context.Background())
	if err != nil {
		t.Fatalf("LedgerEnd: %v", err)
	}
	if got.Offset != 42 {
		t.Errorf("Offset = %d, want 42", got.Offset)
	}
}

// TestLedgerEnd_AttachesBearerToken is the auth-interceptor lock-in.
// Without the interceptor, the fake would see no `authorization` header
// and lastAuth would be empty. The header MUST be `Bearer <token>` not
// `Token <token>` or any other variant the participant's auth-service
// won't accept.
func TestLedgerEnd_AttachesBearerToken(t *testing.T) {
	client, fake, teardown := newTestClient(t, StaticToken("eyJfake.token.value"))
	defer teardown()

	if _, err := client.LedgerEnd(context.Background()); err != nil {
		t.Fatalf("LedgerEnd: %v", err)
	}

	const want = "Bearer eyJfake.token.value"
	if fake.lastAuth != want {
		t.Errorf("server saw authorization=%q, want %q", fake.lastAuth, want)
	}
}

// TestLedgerEnd_NoTokenSourceOmitsHeader pins the "no auth" branch: if
// the caller doesn't pass a TokenSource, no authorization metadata flows.
// This is the contract for participants configured without auth and for
// the bufconn happy-path tests that don't care about JWTs.
func TestLedgerEnd_NoTokenSourceOmitsHeader(t *testing.T) {
	client, fake, teardown := newTestClient(t, nil)
	defer teardown()

	if _, err := client.LedgerEnd(context.Background()); err != nil {
		t.Fatalf("LedgerEnd: %v", err)
	}
	if fake.lastAuth != "" {
		t.Errorf("server saw authorization=%q, expected no header", fake.lastAuth)
	}
}

// TestLedgerEnd_TokenSourceErrorPropagates locks in the failure mode where
// the JWT signer itself fails (e.g. splice.SignToken returns an error
// loading the role's claims). The RPC must NOT be sent without a token;
// the error MUST surface to the caller wrapped, not swallowed.
func TestLedgerEnd_TokenSourceErrorPropagates(t *testing.T) {
	failingToken := TokenSourceFunc(func(context.Context) (string, error) {
		return "", errors.New("sign failed: role unknown")
	})
	client, fake, teardown := newTestClient(t, failingToken)
	defer teardown()

	_, err := client.LedgerEnd(context.Background())
	if err == nil {
		t.Fatal("expected error from token source, got nil")
	}
	if !contains(err.Error(), "sign failed: role unknown") {
		t.Errorf("error chain missing cause: %v", err)
	}
	if fake.lastAuth != "" {
		t.Errorf("server should not have been called; saw auth=%q", fake.lastAuth)
	}
}

// TestLedgerEnd_ServerErrorWrapped pins the wrapping contract: a gRPC
// error from the server reaches the caller via the documented error chain
// (so `errors.Is(err, context.Canceled)` etc. still work upstream).
func TestLedgerEnd_ServerErrorWrapped(t *testing.T) {
	client, fake, teardown := newTestClient(t, nil)
	defer teardown()

	fake.err = errors.New("simulated participant outage")

	_, err := client.LedgerEnd(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "ledger.LedgerEnd:") {
		t.Errorf("error not wrapped with method prefix: %v", err)
	}
}

// TestClose_IsIdempotent — calling Close twice on the same Client must
// not panic and should return at most one real error. gRPC's underlying
// Close is idempotent; this just locks in that we don't add bookkeeping
// that breaks the property.
func TestClose_IsIdempotent(t *testing.T) {
	client, _, teardown := newTestClient(t, nil)
	defer teardown()

	if err := client.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second Close is expected to either no-op or return the documented
	// "already closed" error; both are acceptable. The test only forbids
	// panics.
	_ = client.Close()
}

// TestClose_OnNilClientIsSafe — a Client returned from a failed Dial may
// be nil; Close on nil must not panic. Defends against the common
// `defer c.Close()` pattern where the caller forgot the err check.
func TestClose_OnNilClientIsSafe(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Errorf("Close(nil) returned %v, want nil", err)
	}
}

// contains is a tiny helper to avoid pulling in strings just for one call.
// (Imported in real packages, fine; in tests we inline to keep the file
// import block small.)
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
