package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// sseHandler returns the HTTP handler for /events. Clients connect
// with EventSource and may filter via the `topics` query param
// (comma-separated list; empty = all topics).
//
// Wire format follows the SSE spec (https://html.spec.whatwg.org/multipage/server-sent-events.html):
//
//	id: <Event.ID>\n
//	event: <Event.Topic>\n
//	data: <Event.Data>\n
//	\n
//
// Multi-line payloads get one `data:` line per source line. The
// optional `retry:` field is not sent; the browser default (3s
// reconnect) is fine for a loopback UI.
//
// Headers are flushed immediately so EventSource resolves `onopen`
// quickly, and the subscription is canceled on exit — the critical
// step the godoc on Hub.Subscribe warns about.
//
// Every 30s the handler writes an SSE comment line (`: keepalive\n\n`)
// — invisible to clients per spec, but it keeps the socket warm under
// the typical 60s browser/proxy idle-kill window.
func sseHandler(hub *stream.Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			// Never happens with stdlib http.Server; the assertion
			// makes the assumption explicit.
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// SSE uses GET, which is exempt from the global CSRF
		// middleware — but a tab on evil.example.com can still open
		// EventSource against this endpoint and read the stream, so
		// gate on Origin here. Enforce only when Origin IS present:
		// non-browser clients (curl) send none and proceed.
		if origin := r.Header.Get("Origin"); origin != "" {
			if err := checkOriginAgainstHost(r); err != nil {
				http.Error(w, "forbidden: "+err.Error(),
					http.StatusForbidden)
				return
			}
		}

		h := w.Header()
		// charset=utf-8 is required — without it some clients (older
		// Safari, intermediate proxies) treat the stream as Latin-1
		// and mangle multi-byte event data.
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		// X-Accel-Buffering tells nginx (if it's ever in front of us)
		// not to buffer the response.
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		topics := parseTopics(r.URL.Query().Get("topics"))
		eventCh, cancel := hub.Subscribe(topics...)
		defer cancel()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventCh:
				if !ok {
					return
				}
				writeSSEFrame(w, ev)
				flusher.Flush()
			case <-ticker.C:
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
			}
		}
	})
}

// parseTopics splits a comma-separated topics list, trimming and
// dropping empties. Returns nil for empty input (which the hub
// treats as subscribe-all).
func parseTopics(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// writeSSEFrame emits one event in SSE wire format. Multi-line
// data is split into multiple `data:` lines per spec. Empty Topic
// or ID lines are omitted.
func writeSSEFrame(w http.ResponseWriter, e stream.Event) {
	if e.ID != "" {
		_, _ = fmt.Fprintf(w, "id: %s\n", e.ID)
	}
	if e.Topic != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", e.Topic)
	}
	if len(e.Data) == 0 {
		_, _ = w.Write([]byte("data:\n\n"))
		return
	}
	// Per spec, every line of Data needs its own data: prefix.
	// Strip trailing newlines (callers commonly append "\n" to a log
	// line) so we don't emit a trailing empty `data:` line, which
	// would change the SSE event payload from "msg" to "msg\n".
	body := string(e.Data)
	body = strings.TrimRight(body, "\n")
	for _, line := range strings.Split(body, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = w.Write([]byte("\n"))
}
