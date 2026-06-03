package ledger

import (
	"context"
	"errors"
	"io"
)

// StreamItem is the envelope for each value produced by a server-streaming
// RPC. The package surfaces streams as `<-chan StreamItem[T]` so callers
// don't have to read two channels (one for values, one for errors) and
// race their select cases.
//
// Receive loop pattern:
//
//	ch, err := c.ActiveContracts(ctx, req)
//	if err != nil { return err }       // setup failure (e.g. dial / auth)
//	for item := range ch {
//	    if item.Err != nil { return item.Err } // mid-stream failure
//	    handle(item.Value)
//	}
//	// channel closed: graceful EOF, OR ctx cancellation propagated up
//
// Invariants:
//   - Exactly one of Value / Err is meaningful per item (Err == nil means
//     a real Value; Err != nil means terminal stream failure and the next
//     channel op is a close).
//   - After an Err item, no more Value items arrive and the channel
//     closes immediately (do NOT keep reading expecting recovery).
//   - On ctx.Done(), the pump exits silently without an Err item — the
//     caller already knows ctx is cancelled from its own select cases.
type StreamItem[T any] struct {
	Value T
	Err   error
}

// streamReceiver is the minimal subset of the gRPC server-stream-client
// interface that pumpStream consumes. dazl-client's per-RPC `*XxxClient`
// types each have a `Recv()` method returning (*Response, error); they
// satisfy this interface implicitly via Go's structural typing rules
// because the type parameter T is the response pointer type.
type streamReceiver[T any] interface {
	Recv() (T, error)
}

// streamBufferSize is the channel buffer depth for each open stream.
// Sized to smooth brief consumer stalls without enabling unbounded
// memory growth — the gRPC HTTP/2 flow-control layer below us provides
// the real backpressure when this buffer fills (Recv blocks → no
// WINDOW_UPDATE → server pauses). 32 is a pragmatic default: large
// enough that a typical consumer never sees the producer block on a
// well-behaved network, small enough that worst-case in-flight memory
// is bounded regardless of consumer behaviour.
const streamBufferSize = 32

// pumpStream is the goroutine pattern shared by every server-streaming
// RPC in this package. It opens the stream (delegated via `open`), then
// spins a goroutine that:
//   - Reads from the stream's Recv() in a loop.
//   - Sends each successful Resp downstream wrapped in StreamItem{Value: r}.
//   - On io.EOF, returns silently — the channel close signals end-of-stream.
//   - On any other error, sends one final StreamItem{Err: err} and returns.
//   - On ctx.Done() during a blocking send, returns silently — the caller
//     already saw its own ctx cancellation.
//
// The `open` callback typically calls c.state.GetActiveContracts(ctx, req)
// (or the equivalent on another stub) and returns the resulting stream
// client. Setup errors (auth interceptor failure, deadline exceeded
// before open completes) bubble back synchronously as the error return.
func pumpStream[Resp any](
	ctx context.Context,
	open func(context.Context) (streamReceiver[Resp], error),
) (<-chan StreamItem[Resp], error) {
	stream, err := open(ctx)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamItem[Resp], streamBufferSize)
	go func() {
		defer close(out)
		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				// Try to deliver the terminal error. If ctx is already
				// cancelled the caller has moved on; drop the error
				// rather than leak the goroutine on a full channel.
				select {
				case out <- StreamItem[Resp]{Err: err}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- StreamItem[Resp]{Value: resp}:
			case <-ctx.Done():
				// Caller cancelled mid-stream. Surface ctx.Err() to
				// the consumer (typically context.Canceled or
				// DeadlineExceeded — context.Cause if a custom
				// reason was attached) so they can distinguish a
				// natural EOF from a forced shutdown. (Yellow Y4.)
				cause := context.Cause(ctx)
				if cause == nil {
					cause = ctx.Err()
				}
				select {
				case out <- StreamItem[Resp]{Err: cause}:
				default:
				}
				return
			}
		}
	}()
	return out, nil
}
