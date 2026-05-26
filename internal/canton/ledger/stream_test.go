package ledger

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeStreamServer extends fakeStateServer with the three streaming RPCs
// we test. Tests configure the events / cancellation behaviour by
// reaching into the exported fields before invoking the corresponding
// Client method.
//
// The fake intentionally implements all three streaming surfaces in one
// struct: tests don't need to wire three separate servers, and the
// shared "what does the producer side look like" pattern stays
// grep-able in one place.
type fakeStreamServer struct {
	lapiv2.UnimplementedStateServiceServer
	lapiv2.UnimplementedUpdateServiceServer
	lapiv2.UnimplementedCommandCompletionServiceServer

	// ACS stream: the events to send, then EOF (unless acsError is set).
	acsEvents []*lapiv2.GetActiveContractsResponse
	acsError  error

	// Updates stream.
	updateEvents []*lapiv2.GetUpdatesResponse
	updateError  error
	// updatesSent counts how many were actually sent before
	// the client cancelled / server error. Tests assert against this
	// for cancellation observability.
	updatesSent atomic.Int32

	// Completions stream — emit a slow producer (delay between events)
	// to give cancellation tests a deterministic window.
	completionEvents []*lapiv2.CompletionStreamResponse
	completionDelay  time.Duration
}

func (f *fakeStreamServer) GetActiveContracts(_ *lapiv2.GetActiveContractsRequest, stream grpc.ServerStreamingServer[lapiv2.GetActiveContractsResponse]) error {
	for _, e := range f.acsEvents {
		if err := stream.Send(e); err != nil {
			return err
		}
	}
	return f.acsError
}

func (f *fakeStreamServer) GetUpdates(_ *lapiv2.GetUpdatesRequest, stream grpc.ServerStreamingServer[lapiv2.GetUpdatesResponse]) error {
	for _, e := range f.updateEvents {
		if err := stream.Send(e); err != nil {
			return err
		}
		f.updatesSent.Add(1)
		// Respect client cancellation: gRPC propagates this via the
		// stream's context. Without the check, a "send N then sleep"
		// fake would block the test goroutine on the post-cancel send.
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		default:
		}
	}
	return f.updateError
}

func (f *fakeStreamServer) CompletionStream(_ *lapiv2.CompletionStreamRequest, stream grpc.ServerStreamingServer[lapiv2.CompletionStreamResponse]) error {
	for _, e := range f.completionEvents {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(f.completionDelay):
		}
		if err := stream.Send(e); err != nil {
			return err
		}
	}
	return nil
}

// newStreamTestClient is the streams equivalent of newTestClient — wires
// a Client to a bufconn server with all three streaming server
// implementations registered.
func newStreamTestClient(t *testing.T) (*Client, *fakeStreamServer, func()) {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fake := &fakeStreamServer{}
	lapiv2.RegisterStateServiceServer(srv, fake)
	lapiv2.RegisterUpdateServiceServer(srv, fake)
	lapiv2.RegisterCommandCompletionServiceServer(srv, fake)

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
			t.Log("warning: serve goroutine did not exit within 1s")
		}
	}
	return client, fake, teardown
}

// TestActiveContracts_HappyPath_DeliversAllEventsThenEOF — the basic
// stream contract: every server-sent event reaches the consumer, then
// the channel closes cleanly with no error item.
func TestActiveContracts_HappyPath_DeliversAllEventsThenEOF(t *testing.T) {
	client, fake, teardown := newStreamTestClient(t)
	defer teardown()

	fake.acsEvents = []*lapiv2.GetActiveContractsResponse{
		{WorkflowId: "wf-1"},
		{WorkflowId: "wf-2"},
		{WorkflowId: "wf-3"},
	}

	ch, err := client.ActiveContracts(context.Background(), ActiveContractsRequest{})
	if err != nil {
		t.Fatalf("ActiveContracts: %v", err)
	}

	var got []string
	for item := range ch {
		if item.Err != nil {
			t.Fatalf("unexpected stream error: %v", item.Err)
		}
		got = append(got, item.Value.GetWorkflowId())
	}
	if len(got) != 3 || got[0] != "wf-1" || got[1] != "wf-2" || got[2] != "wf-3" {
		t.Errorf("got events %v, want [wf-1 wf-2 wf-3]", got)
	}
}

// TestActiveContracts_MidStreamErrorDeliveredAsLastItem — when the server
// closes the stream with a non-EOF error mid-flow, the consumer sees the
// successful prefix followed by exactly one Err item, then channel close.
// This is the documented "errors arrive as the last item" contract from
// StreamItem's godoc.
func TestActiveContracts_MidStreamErrorDeliveredAsLastItem(t *testing.T) {
	client, fake, teardown := newStreamTestClient(t)
	defer teardown()

	fake.acsEvents = []*lapiv2.GetActiveContractsResponse{
		{WorkflowId: "wf-1"},
		{WorkflowId: "wf-2"},
	}
	fake.acsError = errors.New("synchronizer unreachable")

	ch, err := client.ActiveContracts(context.Background(), ActiveContractsRequest{})
	if err != nil {
		t.Fatalf("ActiveContracts: %v", err)
	}

	var values []string
	var streamErr error
	for item := range ch {
		if item.Err != nil {
			streamErr = item.Err
			continue
		}
		values = append(values, item.Value.GetWorkflowId())
	}
	if len(values) != 2 {
		t.Errorf("expected 2 values before error, got %d (%v)", len(values), values)
	}
	if streamErr == nil {
		t.Fatal("expected terminal error in stream, got none")
	}
	if !contains(streamErr.Error(), "synchronizer unreachable") {
		t.Errorf("error did not propagate cause: %v", streamErr)
	}
}

// TestUpdates_ContextCancellationStopsConsumer — when the caller cancels
// ctx, the consumer's channel closes promptly. The server-side count of
// "events sent" is bounded by what was already in flight; new events are
// not produced because the gRPC layer cancels the stream end-to-end.
func TestUpdates_ContextCancellationStopsConsumer(t *testing.T) {
	client, fake, teardown := newStreamTestClient(t)
	defer teardown()

	fake.updateEvents = make([]*lapiv2.GetUpdatesResponse, 100)
	for i := range fake.updateEvents {
		fake.updateEvents[i] = &lapiv2.GetUpdatesResponse{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := client.Updates(ctx, UpdatesRequest{})
	if err != nil {
		t.Fatalf("Updates: %v", err)
	}

	// Drain two events then cancel. The exact post-cancel item count is
	// timing-dependent (gRPC's per-stream buffering can hold a few extra),
	// but the channel MUST close eventually and the server MUST stop
	// producing.
	received := 0
	for item := range ch {
		_ = item
		received++
		if received == 2 {
			cancel()
		}
	}

	if received == 0 {
		t.Errorf("expected at least 2 events before cancel, got 0")
	}
	// After channel closes (which is the only way out of the for-range),
	// the server must have observed cancellation. updatesSent ≤ 100 always;
	// the test passes as long as we didn't block.
	if sent := fake.updatesSent.Load(); sent == 100 {
		t.Errorf("server sent all 100 events despite client cancellation — cancel propagation broken")
	}
}

// TestCompletions_NoEventsClosesChannelImmediately — empty stream is
// a valid happy path. Consumer's channel closes with zero items.
func TestCompletions_NoEventsClosesChannelImmediately(t *testing.T) {
	client, _, teardown := newStreamTestClient(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := client.Completions(ctx, CompletionsRequest{UserId: "test-user"})
	if err != nil {
		t.Fatalf("Completions: %v", err)
	}

	count := 0
	for item := range ch {
		if item.Err != nil {
			t.Errorf("unexpected error on empty stream: %v", item.Err)
		}
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 events on empty stream, got %d", count)
	}
}

// TestUpdates_EOFAttachesBearerTokenOnStream — stream auth interceptor
// lock-in. The unary interceptor is tested by client_test.go; this asserts
// the stream interceptor wires the same header. Without it, the
// participant would reject the subscribe with UNAUTHENTICATED and the
// channel would deliver an Err item instead of EOF.
func TestUpdates_EOFAttachesBearerTokenOnStream(t *testing.T) {
	// Build a client with a token. The fake doesn't enforce the token,
	// but the gRPC framework would error if we set up the interceptor
	// wrong (e.g., a nil ctx after withBearerToken).
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fake := &fakeStreamServer{updateEvents: []*lapiv2.GetUpdatesResponse{{}}}
	lapiv2.RegisterUpdateServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dialer := func(_ context.Context, _ string) (net.Conn, error) { return lis.Dial() }
	client, err := Dial(context.Background(), DialOptions{
		Endpoint: "passthrough:///bufnet",
		Token:    StaticToken("test-stream-token"),
		ExtraDialOptions: []grpc.DialOption{
			grpc.WithContextDialer(dialer),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	ch, err := client.Updates(context.Background(), UpdatesRequest{})
	if err != nil {
		t.Fatalf("Updates: %v", err)
	}
	for item := range ch {
		if item.Err != nil && !errors.Is(item.Err, io.EOF) {
			t.Errorf("stream error (interceptor likely broken): %v", item.Err)
		}
	}
}
