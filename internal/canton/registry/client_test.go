package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDial_RejectsBadConfig pins the cheap user-error cases. Empty
// BaseURL, unparseable URL, and non-http(s) scheme all fail at
// construction so the first request doesn't surface a cryptic error.
func TestDial_RejectsBadConfig(t *testing.T) {
	cases := []struct {
		name    string
		opts    DialOptions
		wantSub string
	}{
		{"empty", DialOptions{BaseURL: ""}, "BaseURL is required"},
		{"bad-scheme", DialOptions{BaseURL: "ftp://example"}, "scheme must be http or https"},
		{"unparseable", DialOptions{BaseURL: "::::/x"}, "parse BaseURL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Dial(c.opts)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q missing substring %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestGetDsoInfo_HappyPath exercises the canonical Splice connectivity
// smoke endpoint end-to-end through an httptest server stub. Asserts:
//   - request path matches the documented Splice route
//   - typed response decode preserves the JSON dso_party_id field
//   - bearer token reaches the server's Authorization header
//   - User-Agent and Accept headers are wired
func TestGetDsoInfo_HappyPath(t *testing.T) {
	var capturedAuth, capturedUA, capturedAccept, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedUA = r.Header.Get("User-Agent")
		capturedAccept = r.Header.Get("Accept")
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"dso_party_id": "DSO::1220abc"}`)
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{
		BaseURL: srv.URL,
		Token:   StaticToken("test-jwt"),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	got, err := c.GetDsoInfo(context.Background())
	if err != nil {
		t.Fatalf("GetDsoInfo: %v", err)
	}
	if got.DsoParty != "DSO::1220abc" {
		t.Errorf("DsoParty = %q, want %q", got.DsoParty, "DSO::1220abc")
	}
	if capturedPath != "/api/scan/v0/dso" {
		t.Errorf("server saw path %q, want /api/scan/v0/dso", capturedPath)
	}
	if capturedAuth != "Bearer test-jwt" {
		t.Errorf("server saw Authorization=%q, want Bearer test-jwt", capturedAuth)
	}
	if capturedUA != "canton-devkit/registry" {
		t.Errorf("server saw User-Agent=%q, want canton-devkit/registry", capturedUA)
	}
	if capturedAccept != "application/json" {
		t.Errorf("server saw Accept=%q, want application/json", capturedAccept)
	}
}

// TestGetDsoInfo_OmitsAuthHeaderWhenTokenSourceNil — the "public scan
// endpoint" branch: omitting Token means no Authorization header. This
// matters because some Splice scan endpoints behave subtly differently
// when an unauthenticated client hits them (no party filtering) — the
// contract is "we send no header" not "we send Bearer empty".
func TestGetDsoInfo_OmitsAuthHeaderWhenTokenSourceNil(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"dso_party_id": "DSO::test"}`)
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL}) // no Token
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := c.GetDsoInfo(context.Background()); err != nil {
		t.Fatalf("GetDsoInfo: %v", err)
	}
	if captured != "" {
		t.Errorf("server saw Authorization=%q, expected no header", captured)
	}
}

// TestGetDsoInfo_TokenSourceErrorPropagates — token-source failure
// (e.g. splice.SignToken errors loading a role) MUST surface to the
// caller; the HTTP request MUST NOT fire without auth. Symmetric with
// internal/canton/ledger's TokenSourceErrorPropagates test.
func TestGetDsoInfo_TokenSourceErrorPropagates(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}))
	defer srv.Close()

	failingToken := TokenSourceFunc(func(context.Context) (string, error) {
		return "", errors.New("sign failed")
	})
	c, _ := Dial(DialOptions{BaseURL: srv.URL, Token: failingToken})
	_, err := c.GetDsoInfo(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sign failed") {
		t.Errorf("error chain missing cause: %v", err)
	}
	if hits != 0 {
		t.Errorf("server received %d hits; expected 0 (request should not have fired)", hits)
	}
}

// TestGetDsoInfo_Non2xxYieldsAPIError pins the typed-error contract.
// Splice errors are often informative (4xx with a JSON body explaining
// the rejection); APIError preserves the body so callers can decode
// the typed Splice error shape themselves.
func TestGetDsoInfo_Non2xxYieldsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintln(w, `{"error":"sv_node_down"}`)
	}))
	defer srv.Close()

	c, _ := Dial(DialOptions{BaseURL: srv.URL})
	_, err := c.GetDsoInfo(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Body, "sv_node_down") {
		t.Errorf("Body missing server error message: %q", apiErr.Body)
	}
}

// TestGetDsoInfo_BodyTruncatedAt4KiB locks in the APIError memory-safety
// contract — a misbehaving server returning a 1 GiB error body must not
// OOM the client. The bounded read in doJSON's non-2xx branch is the
// guard.
func TestGetDsoInfo_BodyTruncatedAt4KiB(t *testing.T) {
	// Generate a payload bigger than the limit.
	big := strings.Repeat("X", 8<<10) // 8 KiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	c, _ := Dial(DialOptions{BaseURL: srv.URL})
	_, err := c.GetDsoInfo(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if got := len(apiErr.Body); got > 4<<10 {
		t.Errorf("APIError.Body length = %d bytes, want <= %d (truncation broken)", got, 4<<10)
	}
}
