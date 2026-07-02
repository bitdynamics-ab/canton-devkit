package ledger

import (
	"context"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// CompletionsRequest is the typed input to [Client.Completions].
//
// Use case: after `Submit`-ing a command, the submitter wants to know
// when (and whether) it was committed. CompletionStream emits one
// `Completion` per command ack/nack, filtered by (UserId, Parties).
//
// For the token CLI (`token mint`/`burn`/`transfer`), the pattern is
//  1. Subscribe to Completions for the actAs parties with BeginExclusive
//     = current ledger end.
//  2. Submit the command via [Client.Submit].
//  3. Wait for a matching Completion (CommandId match) or a timeout.
//
// CommandService.SubmitAndWaitForTransaction wraps that pattern as a
// single unary if the caller doesn't want to manage the stream — both
// are exposed; see [Client.SubmitAndWaitForTransaction].
type CompletionsRequest struct {
	// UserId is the application/user identifier the submitter authenticated
	// as. Must match what was on the submission. LocalNet's role-based
	// JWTs (sv-user / app-provider-user / app-user-user from
	// internal/splice/jwt.go) carry the user-id as the `sub` claim.
	UserId string

	// Parties are the actAs parties to filter for. The participant returns
	// completions only for commands submitted by these parties. A typical
	// caller uses the single party they're acting as.
	Parties []string

	// BeginExclusive is the ledger offset to resume from. Use 0 for "from
	// genesis"; use [Client.LedgerEnd] for "from now" (the right choice
	// when subscribing-then-submitting to avoid re-processing old acks).
	BeginExclusive int64
}

// Completions opens a server-streaming subscription on the
// CommandCompletionService. Each StreamItem.Value carries a oneof: a
// Completion event (command ack/nack) or an OffsetCheckpoint heartbeat.
//
// Channel semantics: see [StreamItem]. Cancel via ctx; the participant
// keeps the stream open indefinitely (tail-forever model) — there is no
// EndInclusive equivalent.
func (c *Client) Completions(ctx context.Context, req CompletionsRequest) (<-chan StreamItem[*lapiv2.CompletionStreamResponse], error) {
	r := &lapiv2.CompletionStreamRequest{
		UserId:         req.UserId,
		Parties:        req.Parties,
		BeginExclusive: req.BeginExclusive,
	}
	return pumpStream(ctx, func(ctx context.Context) (streamReceiver[*lapiv2.CompletionStreamResponse], error) {
		return c.cmdComplete.CompletionStream(ctx, r)
	})
}
