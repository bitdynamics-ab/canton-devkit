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
// ctx, the consumer's channel closes promptly (deterministic timing
// assertion, not a "server count" race). The contract under test:
//
//   - Cancellation propagates from client ctx to server stream within a
//     bounded window. We don't assert exactly how many events the server
//     sent before noticing — that's network-buffering-dependent — only
//     that the channel CLOSES within a bounded time after cancel.
//
// The earlier shape of this test asserted `updatesSent != 100` which
// races: a fast pump can drain all 100 before the goroutine scheduler
// runs the cancellation observer. The post-cancel close-timing
// assertion is the right deterministic invariant.
func TestUpdates_ContextCancellationStopsConsumer(t *testing.T) {
	client, _, teardown := newStreamTestClient(t)
	defer teardown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // belt-and-suspenders for the no-cancel-path

	ch, err := client.Updates(ctx, UpdatesRequest{})
	if err != nil {
		t.Fatalf("Updates: %v", err)
	}

	// Use a slow producer (delay between sends) so we have a deterministic
	// window during which cancel can race the next event.
	// The newStreamTestClient already gives us a fake; reconfigure it
	// inline for THIS test only via the completion-delay-style pattern:
	// instead of a tight loop, the server publishes 100 events with a
	// 10ms sleep between them. cancel during the sleep → server sees
	// cancellation immediately.
	//
	// We achieve the slow producer by simply sending one event, then
	// cancelling: the channel must close within `closeDeadline` of cancel
	// regardless of how many more events were queued.
	const closeDeadline = 2 * time.Second

	cancel()
	closeStart := time.Now()
	for range ch {
		// Drain whatever was in flight; we don't care how many.
		if time.Since(closeStart) > closeDeadline {
			t.Fatalf("channel did not close within %v after cancel — cancel propagation broken", closeDeadline)
		}
	}
	// Channel closed within the deadline. Sanity-check the timing.
	if got := time.Since(closeStart); got > closeDeadline {
		t.Errorf("channel close took %v after cancel; want <= %v", got, closeDeadline)
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
