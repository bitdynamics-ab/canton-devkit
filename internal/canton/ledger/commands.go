package ledger

import (
	"context"
	"fmt"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// Submit is fire-and-forget: the participant accepts the command for
// processing and returns immediately. Completion ack/nack arrives later
// via the CommandCompletionService stream (see [Client.Completions]).
//
// Use this when the caller wants explicit control over completion
// waiting — e.g. submitting a batch of commands and only acting on the
// resulting transaction (not the completion). For the simpler
// submit-and-wait pattern, prefer [Client.SubmitAndWaitForTransaction].
//
// The submitter MUST own a JWT issued for the actAs party (LocalNet's
// per-role JWTs in registry.State.Credentials are pre-stocked for the
// sv / app-provider / app-user identities).
func (c *Client) Submit(ctx context.Context, req *lapiv2.SubmitRequest) (*lapiv2.SubmitResponse, error) {
	resp, err := c.cmdSubmit.Submit(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.Submit: %w", err)
	}
	return resp, nil
}

// SubmitAndWaitForTransaction is the "I want the result, not a
// completion stream" convenience. The participant accepts the command,
// waits for it to be committed (or rejected), and returns the
// resulting transaction. Blocks until commit or the request ctx
// deadline expires.
//
// Used by the token CLI (`token create`, `token mint`, etc.) where
// the typical UX is: submit, wait, render the resulting contract IDs.
//
// Caveat: this blocks the gRPC call for the full commit latency
// (typically a few hundred ms on LocalNet, can be seconds on a busy
// synchronizer). Callers issuing many commands in sequence should
// either parallelise via separate goroutines or switch to
// [Client.Submit] + [Client.Completions] for explicit batching.
func (c *Client) SubmitAndWaitForTransaction(ctx context.Context, req *lapiv2.SubmitAndWaitForTransactionRequest) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	resp, err := c.cmd.SubmitAndWaitForTransaction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.SubmitAndWaitForTransaction: %w", err)
	}
	return resp, nil
}

// SubmitAndWait is the lightweight cousin of SubmitAndWaitForTransaction:
// it waits for completion but doesn't return the transaction body. Used
// when the caller doesn't need the resulting events (e.g. an idempotent
// "create if not exists" with the same CommandId).
func (c *Client) SubmitAndWait(ctx context.Context, req *lapiv2.SubmitAndWaitRequest) (*lapiv2.SubmitAndWaitResponse, error) {
	resp, err := c.cmd.SubmitAndWait(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.SubmitAndWait: %w", err)
	}
	return resp, nil
}
