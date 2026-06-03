package registry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestDoJSON_ContextCanceledPropagates: a cancelled context must surface
// as a context.Canceled-matching error (callers cancel mid-flight when a
// CLI/HTTP request is aborted, and the submit layer keys off this).
func TestDoJSON_ContextCanceledPropagates(t *testing.T) {
	c := dialAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the call even starts

	if _, err := c.GetDsoInfo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// TestDoJSON_SuccessBodyCapped: a success body larger than
// maxResponseBytes is truncated by the LimitReader, so the JSON decode
// fails rather than reading an unbounded body into memory.
func TestDoJSON_SuccessBodyCapped(t *testing.T) {
	c := dialAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// {"x":"<more than the cap of 'a's>"} — the closing quote/brace
		// fall beyond the cap, so the decoder hits a truncated value.
		_, _ = io.WriteString(w, `{"x":"`)
		_, _ = io.WriteString(w, strings.Repeat("a", maxResponseBytes+1024))
		_, _ = io.WriteString(w, `"}`)
	}))

	if _, err := c.GetDsoInfo(context.Background()); err == nil {
		t.Fatal("expected a decode error: the over-cap body should be truncated")
	}
}
