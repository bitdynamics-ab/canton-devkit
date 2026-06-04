package registry

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// dialAgainst spins up a test HTTP server and returns a Client wired to
// it. Used by client_cancel_test.go and other low-level client tests.
func dialAgainst(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := Dial(DialOptions{BaseURL: srv.URL, Token: StaticToken("unsafe-test-token")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return c
}
