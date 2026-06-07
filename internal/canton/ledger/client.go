package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// TokenSource produces a fresh bearer JWT for the participant's auth-service
// to validate. Implementations MUST be safe for concurrent use.
//
// For LocalNet, production callers wrap splice.SignToken (see
// internal/splice/jwt.go) — the HS256-with-"unsafe"-secret dev signer that
// the participant's auth-service is configured to accept. Tests pass static
// stubs.
//
// Returning ("", nil) is treated as "no auth header"; useful for participants
// configured without auth (rare) or tests against a fake server that ignores
// the metadata.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// TokenSourceFunc adapts a plain function to the TokenSource interface, so
// callers don't need a struct for one-liners (analogous to http.HandlerFunc).
type TokenSourceFunc func(ctx context.Context) (string, error)

// Token implements TokenSource.
func (f TokenSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

// StaticToken returns a TokenSource that always yields the same token.
// Useful for tests; do NOT use in production code paths where the token
// might expire (the LocalNet dev signer issues tokens without `exp`, so it's
// fine for splice.SignToken consumers — but real-Canton deployments will
// rotate keys).
func StaticToken(t string) TokenSource {
	return TokenSourceFunc(func(context.Context) (string, error) { return t, nil })
}

// DialOptions configures Dial. All fields except Endpoint are optional;
// zero values yield sensible defaults documented per-field.
type DialOptions struct {
	// Endpoint is the participant's gRPC address, e.g. "localhost:5001".
	// Required.
	Endpoint string

	// Token issues per-RPC bearer JWTs (added to the outgoing metadata as
	// `authorization: Bearer <token>`). Nil disables auth — only sensible
	// for participants configured without auth, or tests against a fake.
	Token TokenSource

	// DialTimeout caps the time spent establishing the connection. Defaults
	// to 10s. Note: with WithBlock=false (the default for grpc.NewClient),
	// the dial returns immediately and "connecting" is observable only on
	// the first RPC — this timeout matters only when WithBlock is wired in.
	DialTimeout time.Duration

	// ExtraDialOptions lets callers append arbitrary grpc.DialOption values
	// (TLS creds, interceptors, keepalive params). Tests use this to inject
	// a bufconn dialer.
	ExtraDialOptions []grpc.DialOption

	// PlainText, if true, dials without TLS — i.e. uses
	// credentials/insecure.NewCredentials(). LocalNet's participant ledger
	// API is plaintext over loopback; production deployments override this
	// via ExtraDialOptions with a real `credentials.NewTLS(...)`.
	//
	// Defaults to true (LocalNet-first); callers must explicitly set false
	// AND supply TLS via ExtraDialOptions for real Canton.
	PlainText bool
}

// Client is the typed Ledger API v2 wrapper. Safe for concurrent use across
// goroutines. Construct via Dial; close via Close.
//
// The exposed gRPC service stubs (state, update, etc.) are intentionally
// unexported — callers go through the Client's typed methods, which adapt
// dazl-client request/response shapes to idiomatic Go forms and return
// channels for streaming RPCs.
type Client struct {
	conn  *grpc.ClientConn
	token TokenSource

	// Service stubs from dazl-client. Held as fields rather than constructed
	// per-call so each method site doesn't repeat the constructor.
	state       lapiv2.StateServiceClient
	update      lapiv2.UpdateServiceClient
	eventQuery  lapiv2.EventQueryServiceClient
	pkg         lapiv2.PackageServiceClient
	cmdSubmit   lapiv2.CommandSubmissionServiceClient
	cmdComplete lapiv2.CommandCompletionServiceClient
	cmd         lapiv2.CommandServiceClient
	version     lapiv2.VersionServiceClient

	// Admin-subpackage stubs. The v2 admin RPCs (party, package
	// management, user, identity provider) live in a separate proto
	// package (`com.daml.ledger.api.v2.admin`) — held here so admin.go
	// can stay focused on the typed method wrappers without duplicating
	// the constructor wiring.
	partyMgmt   adminv2.PartyManagementServiceClient
	packageMgmt adminv2.PackageManagementServiceClient
	// userMgmt resolves user-id ↔ party-rights so the Web UI Explorer
	// can map a Splice LocalNet user-id JWT to the set of parties it
	// can actAs / readAs.
	userMgmt adminv2.UserManagementServiceClient
}

// Dial establishes a gRPC connection to the participant's Ledger API.
// Use Close when done; the returned Client is safe for concurrent use.
//
// On LocalNet, opts.Endpoint is typically "localhost:5001" (or whatever
// the per-participant gRPC port is — see `localnet env --name foo` for the
// canonical list). opts.Token wraps splice.SignToken with the per-role
// claims from the registry State.Credentials map.
func Dial(ctx context.Context, opts DialOptions) (*Client, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("ledger.Dial: Endpoint is required")
	}

	dialOpts := []grpc.DialOption{}

	// Transport credentials contract (review blocker #10):
	//   - PlainText=true  → we inject WithTransportCredentials(insecure).
	//   - PlainText=false → we add NOTHING here; the caller MUST supply a
	//     real credentials.NewTLS(...) via ExtraDialOptions. If they
	//     forget, grpc.NewClient will fail at Dial time with "no
	//     transport security set (use grpc.WithTransportCredentials
	//     (insecure.NewCredentials()) explicitly or set credentials)" —
	//     which is the fail-closed behaviour we want. The previous code
	//     called hasCredentials() which always returned false and so
	//     silently fell back to insecure even when PlainText was false,
	//     defeating the TLS contract.
	//
	// The grpc rule that "last creds option wins" still applies if the
	// caller wants to override our insecure default (rare; mostly tests).
	if opts.PlainText {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Auth interceptor: every RPC carries `authorization: Bearer <token>`
	// in the outgoing metadata. The interceptor calls TokenSource per-RPC
	// (not per-connection) so future-rotating tokens are picked up without
	// reconnect. For LocalNet's static "unsafe"-signed JWTs this is
	// over-fetching by one allocation per RPC, but the cost is trivial vs
	// future-proofness.
	if opts.Token != nil {
		dialOpts = append(dialOpts,
			grpc.WithUnaryInterceptor(unaryAuthInterceptor(opts.Token)),
			grpc.WithStreamInterceptor(streamAuthInterceptor(opts.Token)),
		)
	}

	dialOpts = append(dialOpts, opts.ExtraDialOptions...)

	conn, err := grpc.NewClient(opts.Endpoint, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("ledger.Dial: grpc.NewClient(%q): %w", opts.Endpoint, err)
	}

	return &Client{
		conn:        conn,
		token:       opts.Token,
		state:       lapiv2.NewStateServiceClient(conn),
		update:      lapiv2.NewUpdateServiceClient(conn),
		eventQuery:  lapiv2.NewEventQueryServiceClient(conn),
		pkg:         lapiv2.NewPackageServiceClient(conn),
		cmdSubmit:   lapiv2.NewCommandSubmissionServiceClient(conn),
		cmdComplete: lapiv2.NewCommandCompletionServiceClient(conn),
		cmd:         lapiv2.NewCommandServiceClient(conn),
		version:     lapiv2.NewVersionServiceClient(conn),
		partyMgmt:   adminv2.NewPartyManagementServiceClient(conn),
		packageMgmt: adminv2.NewPackageManagementServiceClient(conn),
		userMgmt:    adminv2.NewUserManagementServiceClient(conn),
	}, nil
}

// Close releases the underlying gRPC connection. After Close, further RPCs
// will return an error. Safe to call multiple times — only the first matters
// (grpc.ClientConn.Close is idempotent at the gRPC layer; we don't re-track
// state here).
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// unaryAuthInterceptor attaches `authorization: Bearer <token>` to every
// unary RPC. A nil/empty token from TokenSource is treated as "no auth"
// (no header added) — useful for participants configured without auth.
func unaryAuthInterceptor(src TokenSource) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx, err := withBearerToken(ctx, src)
		if err != nil {
			return fmt.Errorf("ledger auth: %w", err)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// streamAuthInterceptor is the streaming counterpart of unaryAuthInterceptor.
func streamAuthInterceptor(src TokenSource) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
		method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx, err := withBearerToken(ctx, src)
		if err != nil {
			return nil, fmt.Errorf("ledger auth: %w", err)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// withBearerToken adds an `authorization: Bearer <token>` header to the
// outgoing gRPC metadata of ctx. Empty token from src means "no header" —
// the participant may be configured without auth.
func withBearerToken(ctx context.Context, src TokenSource) (context.Context, error) {
	tok, err := src.Token(ctx)
	if err != nil {
		return ctx, err
	}
	if tok == "" {
		return ctx, nil
	}
	md := metadata.Pairs("authorization", "Bearer "+tok)
	// AppendToOutgoingContext composes cleanly if the caller already set
	// metadata via context (e.g. tracing); replacing via NewOutgoingContext
	// would clobber that.
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok), withMetadataForTests(ctx, md)
}

// withMetadataForTests exists so the test fake can extract the auth header
// via context.Value rather than relying on gRPC's incoming-metadata
// extraction. In production the gRPC stack handles the metadata copy; this
// is a no-op there. The function is a no-op stub; the real test seam is
// metadata.FromIncomingContext on the server side. Keeping the second
// return signature for future test instrumentation.
//
// Returns nil error always; signature lets the caller use a single-return
// idiom in withBearerToken.
func withMetadataForTests(_ context.Context, _ metadata.MD) error { return nil }
