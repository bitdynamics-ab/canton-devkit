package token

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestRedactJWTs is the regression guard for the JWT-leak path: even if
// a future change let a bearer token ride out in an error from the
// token-resolution path, redactJWTs must strip it before it reaches the
// user's terminal or logs.
func TestRedactJWTs(t *testing.T) {
	// A realistically-shaped (but fake) HS256 JWT.
	fakeJWT := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.c2lnbmF0dXJlLWJ5dGVz"

	t.Run("masks an embedded token", func(t *testing.T) {
		in := fmt.Errorf("dial failed with token %s: unauthorized", fakeJWT)
		got := redactJWTs(in)
		if strings.Contains(got.Error(), fakeJWT) {
			t.Fatalf("token leaked through redaction: %s", got.Error())
		}
		if !strings.Contains(got.Error(), "[redacted-jwt]") {
			t.Errorf("expected redaction marker, got: %s", got.Error())
		}
		// Non-secret context must be preserved.
		if !strings.Contains(got.Error(), "unauthorized") {
			t.Errorf("dropped surrounding context: %s", got.Error())
		}
	})

	t.Run("nil passes through", func(t *testing.T) {
		if redactJWTs(nil) != nil {
			t.Error("redactJWTs(nil) should be nil")
		}
	})

	t.Run("clean error preserved verbatim (incl. wrapping)", func(t *testing.T) {
		sentinel := errors.New("read instance state: not found")
		got := redactJWTs(fmt.Errorf("wrap: %w", sentinel))
		// No JWT present → original error returned, so %w wrapping (and
		// errors.Is) survives.
		if !errors.Is(got, sentinel) {
			t.Errorf("clean error lost its wrapping chain")
		}
	})
}
