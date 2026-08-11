package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestMapTokenError pins the orchestration-error → HTTP-status mapping,
// including the gRPC-code branch that takes over once the actions submit
// for real (NotFound→404, PermissionDenied→403, Unavailable→503, …).
func TestMapTokenError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"nil → 204", nil, http.StatusNoContent},
		{"needs-v2 → 412", token.ErrNeedsV2LocalNet, http.StatusPreconditionFailed},
		{"unresolved endpoint → 503", token.ErrUnresolvedLedgerEndpoint, http.StatusServiceUnavailable},
		{"plain validation → 400", errors.New("amount must be > 0"), http.StatusBadRequest},
		{"grpc NotFound → 404", status.Error(codes.NotFound, "no such instrument"), http.StatusNotFound},
		{"grpc PermissionDenied → 403", status.Error(codes.PermissionDenied, "nope"), http.StatusForbidden},
		{"grpc Unauthenticated → 403", status.Error(codes.Unauthenticated, "no token"), http.StatusForbidden},
		{"grpc InvalidArgument → 400", status.Error(codes.InvalidArgument, "bad arg"), http.StatusBadRequest},
		{"grpc Unavailable → 503", status.Error(codes.Unavailable, "down"), http.StatusServiceUnavailable},
		{"grpc DeadlineExceeded → 503", status.Error(codes.DeadlineExceeded, "slow"), http.StatusServiceUnavailable},
		{"grpc Internal → 502", status.Error(codes.Internal, "boom"), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mapTokenError(rec, tc.err, "mint")
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// 5xx-class gRPC failures must NOT leak the gRPC message to the client
// (the redaction contract): the body carries only the op summary.
func TestMapTokenError_5xxRedactsDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	mapTokenError(rec, status.Error(codes.Internal, "secret-internal-detail"), "mint")
	if got := rec.Body.String(); strings.Contains(got, "secret-internal-detail") {
		t.Errorf("5xx body leaked the gRPC detail: %s", got)
	}
}
