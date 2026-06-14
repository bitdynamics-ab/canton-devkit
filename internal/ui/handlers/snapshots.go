// Web UI parity for `localnet snapshot` / `localnet restore`.
//
// Two endpoints mounted at /api/instances/:name/snapshot (download)
// and /api/instances/restore (upload). Both delegate to the same
// RunSnapshot / RunRestore functions the CLI calls; the handler is a
// thin spool-via-temp-file shim. We don't refactor the orchestrators
// to take io.Writer/io.Reader directly because:
//
//  1. RunSnapshot writes via an atomic temp-then-rename to defeat
//     partial-write corruption; mirroring that semantic into a
//     streaming HTTP response is a deep refactor for tiny upside
//     (snapshots are 100s of MB, not GBs — disk-spool latency is
//     noise next to docker-archive time).
//  2. RunRestore validates the entire tar header before touching
//     docker; the input MUST be seekable. http.Request.Body is
//     read-once. A temp file is the obvious bridge.
//  3. Keeping a single orchestrator path means the CLI behaviour
//     and the UI behaviour can't drift — same tar layout, same
//     validation, same friendly errors, same exit-code → HTTP
//     status mapping.
//
// # Security
//
//   - CSRF: covered by the global withOriginCheck middleware. POST
//     requires same-Origin.
//   - Path traversal: instance name → validateInstanceName (same
//     guard as registry.Read uses); --from path on restore is
//     handler-controlled (temp file under os.TempDir).
//   - Resource exhaustion: restore upload is capped at
//     restoreUploadMax (4 GiB). A snapshot of a realistic LocalNet
//     fits in 200 MB; 4 GiB is a generous safety net well below
//     the "fill the disk" attacker payload.
//   - Disk leak: every temp file is removed on the defer path,
//     including the error branches.
package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/snapshot"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// contextWithTimeout is the small adapter we use instead of inlining
// context.WithTimeout at every call site — keeps the handler bodies
// flat and lets a future test swap in a shorter clock if needed.
func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// restoreUploadMax is the hard ceiling on the multipart body for a
// restore upload. 4 GiB is comfortably above realistic snapshots
// (sub-200 MB for a healthy pebble-class instance) and comfortably
// below a "fill the disk" attacker payload on a typical dev box.
//
// The number doubles as backpressure: a buggy frontend that tries
// to upload the wrong file (e.g. a postgres dump) hits the cap and
// gets a clean 413 instead of streaming silently for minutes.
const restoreUploadMax = 4 << 30 // 4 GiB

// snapshotTimeout caps the entire snapshot pipeline (registry read +
// pg_dumpall + tar write). Sized to cover a large logical dump on a slow
// disk: a fresh instance dumps in seconds, a heavily-used one in ~tens
// of seconds; 10 min is ample slack without leaving a runaway docker
// process around forever.
const snapshotTimeout = 10 * time.Minute

// restoreTimeout has the same shape but covers loading the dump into a
// throwaway Postgres (container start + psql load).
const restoreTimeout = 10 * time.Minute

// MountSnapshots installs the snapshot/restore endpoints on mux.
//
// Mounted from router.go alongside the other resource Mount* calls.
// hub is currently unused — the snapshot/restore flow is request-
// scoped (not a long-running create-instance-style job), so there's
// no SSE topic. Reserved as a parameter for future "background
// snapshot to /tmp/snapshots" progress streaming.
func MountSnapshots(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/instances/{name}/snapshot", handleSnapshot)
	mux.HandleFunc("POST /api/instances/restore", handleRestore)
}

// handleSnapshot streams a freshly-captured snapshot tar to the
// response. Body shape: gzipped tar (application/gzip). The
// Content-Disposition header carries a suggested filename so the
// browser save dialog defaults sensibly.
//
// Wire shape:
//
//	POST /api/instances/pebble/snapshot
//	-> 200 OK
//	   Content-Type: application/gzip
//	   Content-Disposition: attachment; filename="pebble-2026-05-26.tgz"
//	   <tar bytes>
//
// Errors map to the standard errorBody JSON taxonomy. The most
// common 4xx are NOT_FOUND (instance not in registry) and
// INVALID_REQUEST (instance name fails validation).
func handleSnapshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound,
				"INSTANCE_NOT_FOUND",
				"instance not registered",
				"Run `dpm localnet list` to see available instances.",
			)
			return
		}
		writeError(w, http.StatusInternalServerError, "read registry", err)
		return
	}

	// Spool to a temp file. RunSnapshot writes via atomic
	// rename, so we point it at a path under os.TempDir and
	// stream the result to the response on success.
	tmp, err := os.CreateTemp("", "dpm-snapshot-*.tgz")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create temp", err)
		return
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	// RunSnapshot will overwrite tmpPath via rename; we just
	// needed CreateTemp to mint a unique name. Remove the
	// placeholder so the rename's destination doesn't exist
	// (some filesystems care).
	_ = os.Remove(tmpPath)
	defer func() { _ = os.Remove(tmpPath) }()

	// RunSnapshot writes human progress to out + errors to errw.
	// In the HTTP path we discard both — the structured error
	// response carries enough for the client, and a server-side
	// audit log captures the rest. Capturing errw lets us surface
	// a sensible 500 detail in the server log.
	var errBuf bytes.Buffer
	ctx, cancel := contextWithTimeout(r.Context(), snapshotTimeout)
	defer cancel()
	exit := snapshot.RunSnapshot(ctx, io.Discard, &errBuf, name, tmpPath)
	if exit != localnet.ExitSuccess {
		// A user error (e.g. the instance isn't running, which a database
		// snapshot requires) maps to 400; everything else is a 500.
		status := http.StatusInternalServerError
		if exit == localnet.ExitUserError {
			status = http.StatusBadRequest
		}
		writeError(w, status,
			"snapshot failed", errors.New(strings.TrimSpace(errBuf.String())))
		return
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open snapshot", err)
		return
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stat snapshot", err)
		return
	}

	// Timestamp resolution = seconds so two same-day downloads don't
	// collide in the user's Downloads folder (review yellow).
	filename := fmt.Sprintf("%s-%s.tgz", name, time.Now().UTC().Format("2006-01-02T150405Z"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, filename))
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	// no-store: snapshots include credentials (JWTs in state.json).
	// We don't want browsers or proxies caching them.
	w.Header().Set("Cache-Control", "no-store")
	// CLI↔UI parity: RunSnapshot prints a consistency caveat to stderr
	// when the instance is running, which the CLI user sees but this
	// HTTP path discards (out/errw go to io.Discard). Echo the SAME
	// warning (shared wording via snapshot.RunningSnapshotWarning) in a
	// response header so the Web UI can surface it too, rather than
	// silently capturing a live, possibly-inconsistent instance.
	if state.Status == registry.StatusRunning {
		w.Header().Set("X-Snapshot-Warning", snapshot.RunningSnapshotWarning(name))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// handleRestore consumes a multipart/form-data POST with fields:
//
//	file   — the snapshot tarball (required)
//	name   — target instance name (required)
//	force  — "true" to bypass version-mismatch checks (optional)
//
// Wire shape:
//
//	POST /api/instances/restore
//	Content-Type: multipart/form-data; boundary=...
//	  -> 200 OK
//	     {"name":"pebble","restored":true}
//
// On success the embedded state.json is registered and the database is
// loaded into the instance's Postgres volume; the instance is NOT
// brought up. Caller hits POST /api/instances/{name}/up (existing
// endpoint) to start it on the restored database.
func handleRestore(w http.ResponseWriter, r *http.Request) {
	// Cap the body BEFORE ParseMultipartForm so a malicious or
	// buggy client can't exhaust memory before we get a chance
	// to reject. ParseMultipartForm spools to disk above
	// maxMemory, but we still want a hard ceiling.
	r.Body = http.MaxBytesReader(w, r.Body, restoreUploadMax)
	// 32 MiB in-memory; rest spools to os.TempDir. Tuned so a
	// typical small snapshot (~200 MB) doesn't allocate 200 MB
	// of RAM but doesn't hit disk for a 10 MB test fixture.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErrorWithCode(w, http.StatusRequestEntityTooLarge,
				ErrCodeRequestTooLarge,
				"snapshot upload exceeds size limit",
				fmt.Sprintf("Max upload is %d MiB.", restoreUploadMax>>20),
			)
			return
		}
		writeError(w, http.StatusBadRequest, "parse multipart", err)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	force := r.FormValue("force") == "true"
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}

	// Field naming (yellow Y12): CLI uses --from for the source
	// archive; HTTP multipart historically used "file". We accept
	// BOTH so scripts written against either surface keep working
	// — the CLI mental model ("from where do I restore?") and the
	// HTTP convention ("the file part") are both legitimate.
	file, header, err := r.FormFile("file")
	if err != nil {
		// Try the CLI-aligned alias before giving up.
		var altErr error
		file, header, altErr = r.FormFile("from")
		if altErr != nil {
			writeError(w, http.StatusBadRequest,
				"missing snapshot upload",
				fmt.Errorf("provide the snapshot tarball as multipart field 'file' (or its alias 'from'): %w", err))
			return
		}
	}
	defer func() { _ = file.Close() }()
	if header.Size == 0 {
		writeError(w, http.StatusBadRequest, "file is empty", nil)
		return
	}

	// RunRestore needs a seekable path; spool the upload to
	// a temp file. Capped writer enforces the same ceiling
	// as MaxBytesReader so a multipart envelope can't smuggle
	// extra bytes past the body cap.
	tmpPath, err := spoolToTemp(file, restoreUploadMax)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "spool upload", err)
		return
	}
	defer func() { _ = os.Remove(tmpPath) }()

	var errBuf bytes.Buffer
	ctx, cancel := contextWithTimeout(r.Context(), restoreTimeout)
	defer cancel()
	exit := snapshot.RunRestore(ctx, io.Discard, &errBuf, name, tmpPath, force)
	if exit != localnet.ExitSuccess {
		// User-error exits map to 400. Anything else is a
		// server-side failure (docker outage, disk full,
		// corrupt archive that passed header validation but
		// died mid-extract). The two paths share the same
		// errBuf, so the client gets a stable shape and the
		// distinction lands in the access log.
		status := http.StatusInternalServerError
		if exit == localnet.ExitUserError {
			status = http.StatusBadRequest
		}
		writeError(w, status, "restore failed",
			errors.New(strings.TrimSpace(errBuf.String())))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":     name,
		"restored": true,
	})
}

// spoolToTemp writes r to a fresh temp file, returning the path on
// success. Caller owns os.Remove. cap is the hard ceiling — exceeding
// it returns an error and removes the partial file. We can't fully
// trust http.MaxBytesReader because the multipart parser already
// consumed the envelope bytes by the time we see `file`; this is
// belt-and-braces.
func spoolToTemp(r multipart.File, cap int64) (string, error) {
	tmp, err := os.CreateTemp("", "dpm-restore-*.tgz")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	written, copyErr := io.Copy(tmp, io.LimitReader(r, cap+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", closeErr
	}
	if written > cap {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("upload exceeds %d-byte ceiling", cap)
	}
	return filepath.Clean(tmpPath), nil
}
