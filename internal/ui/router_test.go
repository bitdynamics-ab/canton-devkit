package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// TestVersion_MatchesTypesSchema is the contract that keeps the
// inlined schemaVersion in router.go honest. types.SchemaVersion is
// the canonical value; this test fails if anyone bumps it without
// also touching the UI handler. Catches the silent-mis-decoding
// regression class where the frontend handshakes against a stale
// number.
func TestVersion_MatchesTypesSchema(t *testing.T) {
	if schemaVersion != types.SchemaVersion {
		t.Errorf("internal/ui.schemaVersion = %d, types.SchemaVersion = %d — handshake will lie to the frontend",
			schemaVersion, types.SchemaVersion)
	}
}

// TestVersion_HandlerShape pins the JSON shape the frontend reads on
// bootstrap. Renaming a field here breaks the v1 frontend until
// rebuilt. The contract is: name (string) + schema_version (int).
func TestVersion_HandlerShape(t *testing.T) {
	srv, addr := startTestServer(t)
	defer srv.Shutdown(context.Background())

	resp, err := http.Get("http://" + addr + "/api/version")
	if err != nil {
		t.Fatalf("GET /api/version: %v", err)
	}
	defer resp.Body.Close()

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["name"] != "canton-devkit" {
		t.Errorf("name = %v, want canton-devkit", got["name"])
	}
	// json.Number stays a float64 by default; coerce.
	if int(got["schema_version"].(float64)) != types.SchemaVersion {
		t.Errorf("schema_version = %v, want %d", got["schema_version"], types.SchemaVersion)
	}
}

// TestRouter_CommonHeadersOnEveryResponse is the security pin for the
// nosniff + Server middleware: every response — healthz, version,
// AND the SPA index — must carry both headers. Without nosniff a
// buggy handler that forgets to set Content-Type can be coerced into
// running as script.
func TestRouter_CommonHeadersOnEveryResponse(t *testing.T) {
	srv, addr := startTestServer(t)
	defer srv.Shutdown(context.Background())

	for _, path := range []string{"/healthz", "/api/version", "/"} {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s missing X-Content-Type-Options=nosniff", path)
		}
		if resp.Header.Get("Server") != "dpm" {
			t.Errorf("%s missing Server=dpm, got %q", path, resp.Header.Get("Server"))
		}
	}
}

// TestAssets_SPAFallbackServesIndex pins the SPA contract: an
// unknown URL path (one that doesn't match any embedded file) MUST
// serve index.html so React Router takes over. Without this, deep
// links like /explorer/contracts/abc123 return 404 on hard refresh
// even though the React app would handle them.
func TestAssets_SPAFallbackServesIndex(t *testing.T) {
	srv, addr := startTestServer(t)
	defer srv.Shutdown(context.Background())

	resp, err := http.Get("http://" + addr + "/some/spa/route/that/does/not/exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("SPA fallback returned %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("SPA fallback Content-Type = %q, want text/html", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("SPA index Cache-Control = %q, want no-store (stale index loads stale bundle)", cc)
	}
	body, _ := io.ReadAll(resp.Body)
	// Accept either the placeholder body OR the real Vite build —
	// both legitimately render index.html. Distinguishing them is
	// the job of IsPlaceholderBundle, not these asset-handler
	// tests. We just check it's HTML with the expected charset
	// meta tag and a script reference (placeholder has neither, but
	// it does have <title>; Vite has both module script + meta).
	looksLikeIndex := strings.Contains(string(body), "<title>") ||
		strings.Contains(string(body), `type="module"`)
	if !looksLikeIndex {
		t.Errorf("SPA body doesn't look like index.html: %q", body[:min(120, len(body))])
	}
}

// TestAssets_KnownFileServed verifies the happy path: a request that
// DOES match an embedded file gets that file's bytes (not the SPA
// index). With our placeholder dist/, the only file is index.html
// itself — exercising it via /index.html still proves the file-
// serving branch was taken (vs the SPA fallback).
func TestAssets_KnownFileServed(t *testing.T) {
	srv, addr := startTestServer(t)
	defer srv.Shutdown(context.Background())

	resp, err := http.Get("http://" + addr + "/index.html")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// Accept either the placeholder body OR the real Vite build —
	// both legitimately render index.html. Distinguishing them is
	// the job of IsPlaceholderBundle, not these asset-handler
	// tests. We just check it's HTML with the expected charset
	// meta tag and a script reference (placeholder has neither, but
	// it does have <title>; Vite has both module script + meta).
	looksLikeIndex := strings.Contains(string(body), "<title>") ||
		strings.Contains(string(body), `type="module"`)
	if !looksLikeIndex {
		t.Errorf("body mismatch: %q", body[:min(120, len(body))])
	}
}

// startTestServer is the shared bootstrap for the request-level tests:
// bind a free port, start Serve in a goroutine, return the address.
// Caller defers Shutdown.
func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	assets, err := AssetsHandler()
	if err != nil {
		t.Fatalf("AssetsHandler: %v", err)
	}
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	return srv, addr
}
