package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenSource produces a fresh bearer JWT for the per-request
// `Authorization: Bearer <token>` header. Same contract as
// [internal/canton/ledger.TokenSource] — implementations MUST be
// goroutine-safe. We deliberately don't share the type across
// packages: the two packages are independent dependencies of higher
// layers, and a shared types package just to host this interface
// adds an import-graph entanglement that the duplicate (~5 lines)
// avoids.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// TokenSourceFunc adapts a plain function to TokenSource.
type TokenSourceFunc func(ctx context.Context) (string, error)

// Token implements TokenSource.
func (f TokenSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

// StaticToken always yields the same bearer string. Useful for tests
// and for LocalNet's static "unsafe"-signed JWTs (which don't expire).
// Do NOT use in production paths where the token might rotate.
func StaticToken(t string) TokenSource {
	return TokenSourceFunc(func(context.Context) (string, error) { return t, nil })
}

// DialOptions configures [Dial].
type DialOptions struct {
	// BaseURL is the Splice HTTP root (per-tenant), e.g.
	// "http://localhost:4441" for the app-user tenant on a LocalNet.
	// Per the nginx route map (see doc.go), the same host serves all
	// tenant routes via `/api/<service>/v0/...` prefixes — this is the
	// scheme + host (and optional port), no trailing slash, no path.
	// Required.
	BaseURL string

	// Token sources per-request bearer JWTs. Nil = no Authorization
	// header sent (some scan endpoints are public and accept this; the
	// validator endpoints will reject with 401).
	Token TokenSource

	// HTTPClient lets callers inject a custom http.Client — typically
	// to plug in mTLS, a request-logging transport, or (in tests)
	// httptest's client. Defaults to a fresh http.Client with a 30s
	// per-request Timeout.
	HTTPClient *http.Client

	// UserAgent is appended to every request as the User-Agent header.
	// Helpful for Splice-side log forensics ("which devkit version /
	// command made this call?"). Defaults to "canton-devkit/registry".
	UserAgent string

	// HostHeader, when non-empty, overrides the HTTP Host header sent
	// on every request (req.Host). Needed for Splice LocalNet's nginx
	// virtual-host routing: the scan app's `/registry` routes are gated
	// behind `server_name scan.localhost`, so a request to the SV UI
	// port must carry `Host: scan.localhost` to reach the scan
	// upstream rather than the SV-info default vhost. On a real DevNet
	// (where scan has its own DNS name) this stays empty.
	HostHeader string
}

// Client is the typed Splice HTTP API wrapper. Safe for concurrent use
// across goroutines (the underlying http.Client multiplexes via its
// connection pool).
type Client struct {
	baseURL    string
	token      TokenSource
	http       *http.Client
	userAgent  string
	hostHeader string
}

// Dial constructs a Client. There is no network call here — the
// underlying http.Client connects lazily per-request. Returns an error
// only for client-side configuration mistakes (empty / unparseable
// BaseURL).
func Dial(opts DialOptions) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, errors.New("registry.Dial: BaseURL is required")
	}
	// Validate parseability now so the first request doesn't surface a
	// cryptic url.Parse error from inside the round-trip.
	u, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("registry.Dial: parse BaseURL %q: %w", opts.BaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("registry.Dial: BaseURL scheme must be http or https, got %q", u.Scheme)
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "canton-devkit/registry"
	}

	return &Client{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		token:      opts.Token,
		http:       httpClient,
		userAgent:  ua,
		hostHeader: opts.HostHeader,
	}, nil
}

// doJSON is the centralised request seam for every typed method in this
// package — keeps auth header injection, JSON decode, error shape
// consistent across endpoints (per CLAUDE.md "centralise the seam that
// everyone forgets"). New endpoint methods are typically 5 lines:
// construct path + req body, call doJSON, return decoded result.
//
// The into argument is the decode target (typically `&MyResponse{}`).
// Pass nil if the caller only cares about the HTTP status (rare for
// this API — most endpoints return JSON even for empty responses).
//
// Returns:
//   - nil on 2xx + successful decode.
//   - APIError on non-2xx (with status code + response body for the
//     caller to surface).
//   - wrapped non-API error on network / decode failure.
//
// maxResponseBytes caps the success-path JSON body we'll read from the
// registry. Generous (4 MiB) versus the few-KB choice contexts we
// actually expect, but a hard ceiling against an unbounded/hostile body.
const maxResponseBytes = 4 << 20

func (c *Client) doJSON(ctx context.Context, method, path string, body, into any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("registry.doJSON: marshal body: %w", err)
		}
		reqBody = strings.NewReader(string(raw))
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("registry.doJSON: build request %s %s: %w", method, path, err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Host-header override for nginx virtual-host routing (LocalNet
	// scan registry sits behind `server_name scan.localhost`). Setting
	// req.Host — not req.Header.Set("Host", …) — is the correct knob;
	// net/http reads the Host field, not the header map, for the
	// request line's authority.
	if c.hostHeader != "" {
		req.Host = c.hostHeader
	}
	if c.token != nil {
		tok, err := c.token.Token(ctx)
		if err != nil {
			return fmt.Errorf("registry.doJSON: token source: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("registry.doJSON: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read body (bounded) for the APIError surface — Splice errors
		// are typically JSON-shaped {error, code}, but we don't decode
		// here to avoid masking malformed responses; callers wanting
		// typed errors can `errors.As(err, &reg.APIError{})` and
		// unmarshal `.Body` themselves.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return &APIError{
			Method:     method,
			Path:       path,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	if into == nil {
		return nil
	}
	// Bound the success body too (the error path was already capped). A
	// transfer-factory / choice-context payload is a few KB; cap well
	// above that so a misbehaving or hostile registry can't stream an
	// unbounded body into json.Decode and exhaust memory.
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(into); err != nil {
		return fmt.Errorf("registry.doJSON: decode %s %s response: %w", method, path, err)
	}
	return nil
}

// APIError is returned by every typed method when the Splice server
// responds with a non-2xx status. Callers can `errors.As` to extract
// the response body for further decoding.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string // truncated to 4 KiB for memory safety
}

func (e *APIError) Error() string {
	return fmt.Sprintf("splice %s %s: HTTP %d: %s",
		e.Method, e.Path, e.StatusCode, e.Body)
}
