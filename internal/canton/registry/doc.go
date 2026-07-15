// Package registry is canton-devkit's typed client for the Splice HTTP
// surface — the validator-app and scan-app REST APIs that collectively
// make up the Splice "registry" APIs a LocalNet exposes.
//
// # Surface map (Splice 0.6.4, verified via cached nginx routes)
//
// Per-tenant Splice instance (sv / app-provider / app-user) exposes:
//
//	GET  /api/scan/v0/...        — SV-only; public read-only ledger scan
//	GET  /api/sv/v0/...          — SV-only; super-validator admin
//	*    /api/validator/v0/...   — per-tenant; wallet, transfers, ANS, token ops
//	*    /                       — UI assets (wallet, scan, ANS web UIs)
//	grpc grpc-ledger-api.localhost — Canton Ledger API gRPC (covered by
//	                                  internal/canton/ledger, not this package)
//	http json-ledger-api.localhost — Canton Ledger API JSON
//	                                  (covered by internal/canton/ledger if/when needed)
//
// The CIP-0112 token "registry" is the scan app's `/v0/featured-apps`
// + token-instrument endpoints; per-wallet token operations live on
// `/api/validator/v0/wallet/*` and `/api/validator/v0/transfer/*`.
//
// # Scope
//
// This package ships the wiring (typed Client, auth-header injection,
// JSON decode shape, error wrapping) plus one canonical smoke endpoint
// (`GET /api/scan/v0/dso`) so consumers have a working pattern to
// extend. Per-endpoint methods land alongside the CLI / Web UI consumer
// that needs them — adding a method here without a caller would be
// premature work against an evolving Splice API. See README.md inside
// the package for the "how to add an endpoint" recipe.
//
// # CRITICAL — Host header / virtual-host routing
//
// Splice's nginx config defines multiple `server_name` blocks under one
// listen port. On DevKit LocalNet the blocks are instance-scoped: the
// /api/scan/* routes only match Host `scan.<instance>.localhost`;
// /api/validator/* needs `wallet.<instance>.localhost` (per-tenant
// routing). With `Host: localhost` (the implicit default when BaseURL
// is `http://localhost:PORT`), requests fall through to the default
// server block which serves the tenant's SPA HTML — your 200-OK
// response will be `<!DOCTYPE html>` instead of JSON, and the decode
// error message is opaque.
//
// Pass the right virtual host via BaseURL, e.g. for instance "dev":
//
//	registry.Dial(registry.DialOptions{
//	    BaseURL: "http://scan.dev.localhost:<sv-port>",        // for /api/scan/*
//	    BaseURL: "http://wallet.dev.localhost:<tenant-port>",  // for /api/validator/*
//	})
//
// *.localhost resolves to 127.0.0.1 in browsers, curl, and Go, but NOT
// in the JVM/Node/Python resolvers (and some Rust HTTP clients). Go's
// net resolver handles the `.localhost` suffix, so this client works
// out of the box; from other runtimes set the Host header explicitly
// (or add an /etc/hosts entry) via a wrapping http.RoundTripper in
// DialOptions.HTTPClient.
//
// # Auth
//
// Identical model to [internal/canton/ledger]: the [TokenSource]
// interface returns a fresh bearer JWT per-request. Production wraps
// [internal/splice.SignToken]; tests pass [StaticToken].
//
// # Tests
//
// Unit tests use [httptest.NewServer] with route-matched stub handlers.
// Integration tests behind `//go:build integration` hit a real
// `localnet up --name dev` instance — same gating as ledger/.
package registry
