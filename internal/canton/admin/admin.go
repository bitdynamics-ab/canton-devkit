// Package admin is a thin gRPC client for Canton's Participant Admin
// API. It wraps the generated `proto` subpackage with connection
// helpers (TLS / plaintext, bearer-token auth) and method-shim
// signatures the CLI commands can call without dragging gRPC details
// into the command layer.
//
// Vendored proto: community/admin-api/.../v30/package_service.proto
// from digital-asset/canton (Apache 2.0). Regenerate with `make
// admin-proto` (see Makefile target) when the upstream proto changes.
package admin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Config is the bag of options passed to Connect.
type Config struct {
	// Host is "<addr>:<port>", e.g. "localhost:2902".
	Host string
	// Token is an optional bearer token. Sent as "authorization:
	// Bearer <token>" on every RPC when non-empty. Splice LocalNet
	// uses HS256-signed JWTs with the "unsafe" dev secret — see
	// internal/splice/jwt.go for the signer.
	Token string
	// Insecure, when true, disables TLS. Splice LocalNet runs
	// plaintext gRPC by default, so this is the common case for dev
	// work. Production deployments should leave this false and
	// configure CACertFile / system trust.
	Insecure bool
	// CACertFile is a path to a PEM-encoded root CA bundle. Used
	// when Insecure is false. Empty means use the system trust pool.
	CACertFile string
	// ServerName overrides the SNI / cert-validation host name.
	// Useful when reaching a participant via a tunnelled hostname
	// that doesn't match its cert.
	ServerName string
}

// Client is a connected handle to a Canton participant's Admin API.
// One Client per logical instance — keep them alive across calls so
// HTTP/2 + TLS handshakes amortise.
type Client struct {
	cc      *grpc.ClientConn
	Package adminproto.PackageServiceClient
}

// Connect dials the participant Admin API. Caller must call Close.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("canton admin: --admin-host is required")
	}

	creds, err := transportCreds(cfg)
	if err != nil {
		return nil, err
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		// Canton DARs can be 10s of MB; bump the default 4MiB receive
		// limit to 100MiB which covers everything we've seen in
		// Splice.
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100 * 1024 * 1024)),
	}
	if cfg.Token != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(bearerToken(cfg.Token, cfg.Insecure)))
	}

	cc, err := grpc.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.Host, err)
	}
	_ = ctx // grpc.NewClient is lazy; ctx is honoured by individual RPCs
	return &Client{
		cc:      cc,
		Package: adminproto.NewPackageServiceClient(cc),
	}, nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.cc == nil {
		return nil
	}
	return c.cc.Close()
}

// NewWithConn wraps an existing *grpc.ClientConn into a Client. Used by
// tests (bufconn) where the connection is pre-built; production code
// should call Connect.
func NewWithConn(cc *grpc.ClientConn) *Client {
	return &Client{
		cc:      cc,
		Package: adminproto.NewPackageServiceClient(cc),
	}
}

// transportCreds chooses between insecure, system-CA-backed TLS, and
// custom-CA TLS based on Config.
func transportCreds(cfg Config) (credentials.TransportCredentials, error) {
	if cfg.Insecure {
		return insecure.NewCredentials(), nil
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.ServerName,
	}
	if cfg.CACertFile != "" {
		pem, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %s: %w", cfg.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no PEM certs found in %s", cfg.CACertFile)
		}
		tlsCfg.RootCAs = pool
	}
	return credentials.NewTLS(tlsCfg), nil
}

// bearerToken implements grpc.PerRPCCredentials for the standard
// "authorization: Bearer <token>" header pattern. Refuses to attach
// the token over an insecure (plaintext) channel UNLESS the caller
// opted in via Config.Insecure — Splice LocalNet is plaintext by
// design so we have to allow it, but we keep the gate explicit.
type bearerCreds struct {
	token            string
	allowInsecureTLS bool
}

func bearerToken(token string, allowInsecure bool) credentials.PerRPCCredentials {
	return &bearerCreds{token: token, allowInsecureTLS: allowInsecure}
}

func (b *bearerCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}

func (b *bearerCreds) RequireTransportSecurity() bool {
	return !b.allowInsecureTLS
}

// WithAuthMetadata returns a context whose outgoing gRPC metadata
// carries the token. Used by call sites that need to attach auth
// without going through PerRPCCredentials (rare, but useful for
// one-off probes from CLI flags).
func WithAuthMetadata(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}
