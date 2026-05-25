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
// Multi-line payloads get one `data:` line per source line. We do NOT
// implement the optional `retry:` field; browsers default to 3s
// reconnect which is fine for a loopback UI.
//
// # Connection lifecycle
//
// The handler:
//  1. Sets SSE headers (Content-Type, no caching, keep-alive hint).
//  2. Flushes them immediately so the browser's EventSource resolves
//     `onopen` quickly.
//  3. Subscribes to the hub for the requested topics.
//  4. Loops: read from the subscription channel, write SSE frame,
//     Flush. Exits when the channel closes OR the request context
//     is canceled (client disconnect).
//  5. Calls cancel on exit to free the subscription. This is the
//     critical step the godoc on Hub.Subscribe warns about.
//
// # Heartbeat
//
// Every 30s the handler writes an SSE comment line (`: keepalive\n\n`).
// Browsers and intermediate proxies that aren't told otherwise will
// drop an idle TCP connection after 60s; the comment is invisible to
// the client (the spec says lines starting with `:` are ignored) but
// keeps the socket warm. Tuned to be well under the typical 60s
// idle-kill window.
func sseHandler(hub *stream.Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			// This should never happen with stdlib http.Server but
			// the type assertion makes the assumption explicit.
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Reviewer pin (PR #42 #c): SSE uses GET, which is exempt
		// from the global CSRF middleware. But the threat model
		// still applies — a tab on evil.example.com can open
		// EventSource("http://127.0.0.1:7777/events") and read
		// our event stream (Origin is sent but not checked by
		// EventSource itself). Gate explicitly here.
		//
		// Origin is missing on direct curl (no browser), so we
		// only enforce when it IS present and only fail on
		// mismatch — curl users with no Origin proceed.
		if origin := r.Header.Get("Origin"); origin != "" {
			if err := checkOriginAgainstHost(r); err != nil {
				http.Error(w, "forbidden: "+err.Error(),
					http.StatusForbidden)
				return
			}
		}

		// SSE headers.
		h := w.Header()
		// Reviewer pin (PR #42 round-2 #4): charset=utf-8 is
		// required — without it some clients (older Safari,
		// intermediate proxies) treat the stream as Latin-1
		// and mangle multi-byte event data.
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		// X-Accel-Buffering tells nginx (if it's ever in front of us)
		// not to buffer the response. Loopback-only today but cheap
		// future-proofing.
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Topic filter from query string.
		topics := parseTopics(r.URL.Query().Get("topics"))
		eventCh, cancel := hub.Subscribe(topics...)
		defer cancel()

		// Heartbeat — see godoc. Pulled out so the select reads
		// cleanly.
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
	// Strip a single trailing newline (the most common case where
	// callers append "\n" to a log line) so we don't emit a
	// trailing empty `data:` line, which would change the SSE
	// event payload from "msg" to "msg\n" — reviewer pin (PR #42
	// round-2 #4 trailing-newline bug).
	body := string(e.Data)
	body = strings.TrimRight(body, "\n")
	for _, line := range strings.Split(body, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = w.Write([]byte("\n"))
}
