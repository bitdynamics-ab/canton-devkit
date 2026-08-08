package token

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchDAR_404IsNotPublished pins that a 404 from the upstream repo
// surfaces as errDARNotPublished (so ensureTokenDARs can translate it
// into the actionable ErrTokenDARUnavailable) rather than a raw HTTP
// error string. This is the splice-test-token-v2 case for a pre-0.6.11
// release commit (e.g. 0.6.4).
func TestFetchDAR_404IsNotPublished(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	old := darBundleBaseURL
	darBundleBaseURL = srv.URL
	defer func() { darBundleBaseURL = old }()

	if _, err := fetchDAR(context.Background(), "deadbeef", "splice-test-token-v2-1.0.0.dar"); !errors.Is(err, errDARNotPublished) {
		t.Fatalf("404 should map to errDARNotPublished, got %v", err)
	}
}

// TestFetchDAR_200ReturnsBytes pins the happy path through the base-URL seam.
func TestFetchDAR_200ReturnsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("dar-bytes"))
	}))
	defer srv.Close()
	old := darBundleBaseURL
	darBundleBaseURL = srv.URL
	defer func() { darBundleBaseURL = old }()

	b, err := fetchDAR(context.Background(), "abc", "x.dar")
	if err != nil {
		t.Fatalf("fetchDAR: %v", err)
	}
	if string(b) != "dar-bytes" {
		t.Errorf("body = %q, want dar-bytes", b)
	}
}

// TestDARUnavailableError pins the actionable message: it wraps
// ErrTokenDARUnavailable and names the version + stable-version remedy +
// the manual-upload escape hatch.
func TestDARUnavailableError(t *testing.T) {
	err := darUnavailableError("splice-test-token-v2", "0.6.4")
	if !errors.Is(err, ErrTokenDARUnavailable) {
		t.Fatalf("should wrap ErrTokenDARUnavailable, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"splice-test-token-v2", "0.6.4", "0.6.11", "dar upload"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}
