package godamlprobe

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProbe_RequiresEndpoint(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := Probe(ctx, Options{Token: "unused"})
	if err == nil {
		t.Fatal("Probe with empty endpoint: want error, got nil")
	}
	if !strings.Contains(err.Error(), "endpoint is required") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "endpoint is required")
	}
}

func TestProbe_UnreachableEndpoint(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Reserved TEST-NET-1 address that is not listening locally.
	_, err := Probe(ctx, Options{
		Endpoint: "127.0.0.1:1",
		Token:    "not-a-real-token",
	})
	if err == nil {
		t.Fatal("Probe against closed port: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "godamlprobe.Probe") {
		t.Fatalf("error = %q, want godamlprobe.Probe prefix", msg)
	}
}
