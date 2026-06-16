package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// TestMapTokenError_DARUnavailableMaps412 pins that the
// test-token-DAR-unavailable sentinel surfaces as 412 with the
// TEST_TOKEN_DAR_UNAVAILABLE code (the UI/CLI uses the message to point
// the user at a token-standard-v2 instance) — not a raw GitHub 404.
func TestMapTokenError_DARUnavailableMaps412(t *testing.T) {
	rec := httptest.NewRecorder()
	mapTokenError(rec, token.ErrTokenDARUnavailable, "create")

	if rec.Code != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "TEST_TOKEN_DAR_UNAVAILABLE") {
		t.Errorf("body should carry TEST_TOKEN_DAR_UNAVAILABLE code; got %s", rec.Body.String())
	}
}
