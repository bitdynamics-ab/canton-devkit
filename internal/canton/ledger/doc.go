// Package ledger is canton-devkit's typed client for the Daml Ledger API v2
// (`com.daml.ledger.api.v2`). It wraps the gRPC stubs from
// github.com/digital-asset/dazl-client/v8 in idiomatic Go signatures:
// channels for server-streaming RPCs, plain returns for unaries.
//
// # Scope
//
// Coverage matches the proposal (docs/original-devkit-proposal.md, line 175):
// *"Backend uses Ledger API v2: `StateService.GetActiveContracts`,
// `UpdateService.GetUpdates`, and `EventQueryService`, with
// `PackageService` + DAR metadata"*, extended to every RPC the M2 Web UI
// Explorer (contracts watch, tx ls/replay, contract detail drawer) and the
// M3 token CLI (`token create/mint/transfer/burn`) need:
//
//	StateService              — GetLedgerEnd, GetActiveContracts (stream),
//	                            GetConnectedSynchronizers
//	UpdateService             — GetUpdates (stream), GetUpdateTrees (stream),
//	                            GetTransactionById, GetTransactionByOffset
//	EventQueryService         — GetEventsByContractId
//	PackageService            — ListPackages, GetPackage, GetPackageStatus
//	PartyManagementService    — ListKnownParties, AllocateParty
//	CommandSubmissionService  — Submit, SubmitAndWaitForTransaction
//	CommandCompletionService  — CompletionStream (stream)
//
// # Auth
//
// The Daml Ledger API v2 expects a bearer JWT on every RPC (the participant's
// auth-service validates it). The client takes a [TokenSource] interface so
// production callers can inject splice.SignToken (LocalNet's "unsafe" dev
// signer documented in internal/splice/jwt.go) and tests can pass static
// stubs. The interface keeps internal/canton/ledger reusable for any future
// non-LocalNet ledger.
//
// # Stream shape
//
// Server-streaming RPCs return a receive-only channel and an in-flight
// error channel; callers select on both plus their own cancellation:
//
//	updates, errs, err := c.Updates(ctx, req)
//	if err != nil { ... }
//	for {
//	    select {
//	    case u, ok := <-updates:
//	        if !ok { return nil } // EOF
//	        handle(u)
//	    case e := <-errs:
//	        return e
//	    case <-ctx.Done():
//	        return ctx.Err()
//	    }
//	}
//
// Backpressure is the caller's problem — this package does NOT drop events.
// (The SSE hub in internal/ui/stream does drop-oldest for slow consumers;
// adaptation happens at the layer that fans into it, not here.)
//
// # Concurrency
//
// A single Client is safe for concurrent use across goroutines. Underlying
// gRPC connections multiplex naturally; the TokenSource must also be
// goroutine-safe.
//
// # Testing
//
// Unit tests use google.golang.org/grpc/test/bufconn with a fake server
// implementing the dazl-client server interfaces. Real-Canton integration
// tests are behind `//go:build integration` and require `localnet up --name dev`.
package ledger
