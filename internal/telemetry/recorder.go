package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"
)

const (
	// spoolCap bounds the local spool so a long-offline machine never
	// grows it without limit; the oldest events are dropped first.
	spoolCap = 500
	// flushBatch is how many spooled events trigger a network flush.
	// Most commands just append; roughly 1-in-flushBatch flushes.
	flushBatch = 20
	// flushTimeout hard-caps the user-visible delay a flush can add.
	flushTimeout = 1200 * time.Millisecond
	// flushMaxAge flushes a non-empty spool whose oldest event is older
	// than this, so low-frequency users (who never hit flushBatch) still
	// eventually report.
	flushMaxAge = 24 * time.Hour
)

// RecordCommand is the single hook the CLI calls after a command runs. It
// is best-effort and MUST NOT block meaningfully or ever return an error
// that the caller has to handle — telemetry failures are invisible to the
// user. Honors the env/config opt-out.
func RecordCommand(toolVersion, commandPath string, exitCode int, dur time.Duration) {
	if !Enabled() {
		return
	}
	ev := Event{
		SchemaVersion:  SchemaVersion,
		InstallID:      LoadConfig().InstallID,
		Event:          "command",
		Command:        commandPath, // PATH ONLY — caller must not pass args/flags
		ExitCode:       exitCode,
		DurationBucket: durationBucket(dur),
		ToolVersion:    toolVersion,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := appendSpool(ev); err != nil {
		return // give up silently
	}
	// Flush opportunistically when the batch threshold is reached OR the
	// spool's oldest event is stale — so we don't make a network call on
	// every command, but low-frequency users still report. Synchronous
	// but hard-timeout-bounded; failure leaves events spooled.
	if shouldFlush() {
		_ = Flush(context.Background())
	}
}

// shouldFlush reports whether the spool is ready to send: at least
// flushBatch events, or a non-empty spool older than flushMaxAge.
func shouldFlush() bool {
	events, err := readSpool()
	if err != nil || len(events) == 0 {
		return false
	}
	if len(events) >= flushBatch {
		return true
	}
	if ts, perr := time.Parse(time.RFC3339, events[0].Timestamp); perr == nil {
		return time.Since(ts) > flushMaxAge
	}
	return false
}

// appendSpool appends one event as a JSON line, trimming to spoolCap.
func appendSpool(ev Event) error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	events, _ := readSpool()
	events = append(events, ev)
	if len(events) > spoolCap {
		events = events[len(events)-spoolCap:]
	}
	return writeSpool(events)
}

func readSpool() ([]Event, error) {
	f, err := os.Open(spoolPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev Event
		if json.Unmarshal(line, &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}

func writeSpool(events []Event) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return os.WriteFile(spoolPath(), buf.Bytes(), 0o644)
}

// resolveEndpoint returns the effective collector URL: env override wins
// over the build-time default. Empty = spool-only.
func resolveEndpoint() string {
	if e := os.Getenv(envEndpoint); e != "" {
		return e
	}
	return endpoint
}

// Flush posts the spooled events to the collector as one batch and, on a
// 2xx response, clears the spool. No endpoint configured, or any error,
// leaves the spool intact for a later attempt. Hard-timeout-bounded.
func Flush(ctx context.Context) error {
	url := resolveEndpoint()
	if url == "" {
		return nil // spool-only mode; nothing leaves the machine
	}
	events, err := readSpool()
	if err != nil || len(events) == 0 {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"schema_version": SchemaVersion,
		"events":         events,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "canton-devkit-telemetry/1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err // network down / timeout — keep the spool
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return errBadStatus(resp.StatusCode)
	}
	// Delivered — clear the spool (truncate to empty).
	return os.Remove(spoolPath())
}

type errBadStatus int

func (e errBadStatus) Error() string { return "telemetry collector returned non-2xx" }

// RecentEvents returns the spooled events for `telemetry status` so a
// user can audit exactly what is queued / would be sent. Read-only.
func RecentEvents() ([]Event, error) { return readSpool() }

// EffectiveEndpoint is exposed for `telemetry status` so a user sees
// whether (and where) data is sent. Returns "" for spool-only.
func EffectiveEndpoint() string { return resolveEndpoint() }
