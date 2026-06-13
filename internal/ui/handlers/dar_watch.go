// DAR hot-deploy SSE bridge.
//
// `dpm localnet dar watch` is a separate CLI process from the
// `dpm localnet ui` server, so it can't publish to the same
// in-process *stream.Hub directly. The bridge uses a small
// loopback HTTP endpoint:
//
//	POST /api/dar/watch/publish
//	  body {instance, dar_id?, event: "rebuild_started"|
//	         "rebuild_finished"|"upload_finished", at: <unix-ts>,
//	         detail?: string}
//	  → 202 (best-effort; no body)
//
//	GET /api/dar/watch/events?instance=<i>&dar=<id>
//	  SSE stream of the same JSON payloads. Topic filter is
//	  applied server-side from the query params; the wire frame is
//	  the raw event body.
//
// The publish endpoint is loopback-only (gated by the existing
// withOriginCheck middleware on the rest of the API). The dar
// watch process discovers the UI URL via the registry's recorded
// ui_url field — falling back to a noisy stderr warning when the
// UI isn't running, so the watch loop still functions standalone.
//
// Topic format: `dar:watch:<instance>:<dar-id>`. A trailing
// dar-id of "*" subscribes
// to every DAR for the instance, used by the DAR screen when no
// specific row is expanded.

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// watchHub is the slice of *stream.Hub the watch handlers need. We
// keep it as an interface so test wiring can inject a fake without
// importing the real hub. The signature mirrors the methods we
// actually call.
type watchHub interface {
	Publish(stream.Event) int
	Subscribe(topics ...string) (<-chan stream.Event, func())
}

// watchPublishMax is the JSON-body cap on the publish endpoint.
// Events are tiny (<200 bytes typical); 64 KiB matches the rest of
// the handlers/ package.
const watchPublishMax = 64 << 10

// validWatchEvents pins the event-string enum the publisher accepts.
// Rejecting unknown values up-front means a typo in the CLI doesn't
// silently emit ghost events that no consumer renders.
var validWatchEvents = map[string]bool{
	"rebuild_started":  true,
	"rebuild_finished": true,
	"upload_finished":  true,
	"watch_started":    true,
	"watch_stopped":    true,
}

// watchEvent is the wire shape published by the CLI and re-emitted
// as the SSE data payload. SchemaVersion is set server-side on
// re-emit so the client never has to trust the publisher's value.
type watchEvent struct {
	Instance string `json:"instance"`
	DarID    string `json:"dar_id,omitempty"`
	Event    string `json:"event"`
	At       int64  `json:"at"`
	Detail   string `json:"detail,omitempty"`
}

// WatchTopic returns the SSE topic format. Exported so the CLI's
// watch.go can construct the same string when publishing. Format:
// `dar:watch:<instance>:<dar-id>`.
func WatchTopic(instance, darID string) string {
	if darID == "" {
		darID = "*"
	}
	return "dar:watch:" + instance + ":" + darID
}

// handleDARWatchPublish receives an event from the CLI watch
// process and re-publishes it on the hub. Returns 202 Accepted on
// success — the publisher doesn't care if any subscriber is
// listening (fire-and-forget).
func handleDARWatchPublish(hub watchHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, watchPublishMax)
		var body watchEvent
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "decode body", err)
			return
		}
		if err := registry.ValidateName(body.Instance); err != nil {
			writeError(w, http.StatusBadRequest, "invalid instance name", err)
			return
		}
		if !validWatchEvents[body.Event] {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"unknown event: "+body.Event,
				"event must be one of rebuild_started, rebuild_finished, upload_finished, watch_started, watch_stopped")
			return
		}
		if body.At == 0 {
			body.At = time.Now().Unix()
		}
		// Light sanity on dar_id; CLI sends "" when the build hasn't
		// produced a DAR yet (initial watch_started). Pass through
		// when empty — the topic-fanout falls back to the wildcard.
		if body.DarID != "" && !looksLikePackageID(body.DarID) {
			// Don't fail the publish — just sanitise; some watch
			// emissions carry the dar-file basename rather than a
			// package id, and the UI handles that.
			body.DarID = strings.TrimSpace(body.DarID)
		}

		payload, err := json.Marshal(body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal event", err)
			return
		}
		// Publish to both the per-DAR topic and the wildcard topic,
		// so a "show all" subscriber doesn't miss a per-DAR event.
		hub.Publish(stream.Event{
			SchemaVersion: stream.EventSchemaVersion,
			Topic:         WatchTopic(body.Instance, body.DarID),
			Data:          payload,
		})
		if body.DarID != "" {
			hub.Publish(stream.Event{
				SchemaVersion: stream.EventSchemaVersion,
				Topic:         WatchTopic(body.Instance, ""), // wildcard
				Data:          payload,
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// handleDARWatchEvents is the SSE subscriber side. Mirrors the
// per-instance SSE handler's lifecycle (subscribe → heartbeat →
// drain on disconnect).
func handleDARWatchEvents(hub watchHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instance := r.URL.Query().Get("instance")
		dar := r.URL.Query().Get("dar")
		if err := registry.ValidateName(instance); err != nil {
			writeError(w, http.StatusBadRequest, "invalid instance name", err)
			return
		}
		if dar != "" && !looksLikePackageID(dar) && dar != "*" {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest, "invalid dar id")
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming unsupported", nil)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		topic := WatchTopic(instance, dar)
		ch, cancel := hub.Subscribe(topic)
		defer cancel()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if len(ev.Data) == 0 {
					_, _ = w.Write([]byte("data:\n\n"))
				} else {
					for _, line := range strings.Split(strings.TrimRight(string(ev.Data), "\n"), "\n") {
						_, _ = fmt.Fprintf(w, "data: %s\n", line)
					}
					_, _ = w.Write([]byte("\n"))
				}
				flusher.Flush()
			case <-ticker.C:
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
			}
		}
	}
}

// Compile-time check that *stream.Hub satisfies watchHub. Surfaces
// a signature drift early if the hub gains/drops a method.
var _ watchHub = (*stream.Hub)(nil)
