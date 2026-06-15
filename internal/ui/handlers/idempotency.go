package handlers

import (
	"bytes"
	"net/http"
	"sync"
	"time"
)

// Idempotency-Key support for the state-changing token POSTs (mint /
// transfer / burn / accept / create). Opt-in: a request that carries an
// `Idempotency-Key` header is deduplicated; one that doesn't is handled
// normally. This lets a client safely retry a POST (network blip, double
// click) without minting/transferring/burning twice.
//
// Semantics:
//   - first request for a key: runs the handler, caches the full response.
//   - replay of a completed key: returns the cached response verbatim,
//     with `Idempotency-Replayed: true`.
//   - replay while the first is still in flight: 409 Conflict, so a
//     concurrent double-submit can't execute the mutation twice.
//   - a 5xx response is NOT cached: a transient upstream blip (e.g. the
//     participant briefly returning 503) must not pin a stale failure
//     for the whole TTL — the key is freed so a retry re-attempts the
//     mutation rather than replaying the error.
//
// The cache is in-process (a single devkit serves one user's loopback
// UI) with a TTL; it is NOT a distributed/durable idempotency store and
// doesn't survive a restart — appropriate for LocalNet tooling.

const idempotencyTTL = 10 * time.Minute

type idemEntry struct {
	done    bool
	status  int
	ctype   string
	body    []byte
	created time.Time
}

type idemStore struct {
	mu      sync.Mutex
	entries map[string]*idemEntry
	now     func() time.Time // injectable for tests
}

func newIdemStore() *idemStore {
	return &idemStore{entries: make(map[string]*idemEntry), now: time.Now}
}

// wrap decorates a POST handler with Idempotency-Key dedup.
func (s *idemStore) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next(w, r) // idempotency is opt-in
			return
		}
		// Namespace by method+path so the same key reused on /mint and
		// /burn doesn't collide.
		ck := r.Method + " " + r.URL.Path + " " + key

		s.mu.Lock()
		s.gcLocked()
		if e, ok := s.entries[ck]; ok {
			if e.done {
				s.mu.Unlock()
				s.replay(w, e)
				return
			}
			s.mu.Unlock()
			writeErrorWithCode(w, http.StatusConflict, "IDEMPOTENCY_IN_FLIGHT",
				"a request with this Idempotency-Key is already in flight")
			return
		}
		e := &idemEntry{created: s.now()}
		s.entries[ck] = e
		s.mu.Unlock()

		rec := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)

		s.mu.Lock()
		if rec.status >= 500 {
			// Don't cache a transient failure: drop the placeholder so the
			// next request with this key re-runs the handler instead of
			// replaying the 5xx for the rest of the TTL. The in-flight
			// guard above still serialises concurrent duplicates while the
			// first attempt runs.
			delete(s.entries, ck)
			s.mu.Unlock()
			return
		}
		e.done = true
		e.status = rec.status
		e.ctype = w.Header().Get("Content-Type")
		e.body = rec.buf.Bytes()
		s.mu.Unlock()
	}
}

func (s *idemStore) replay(w http.ResponseWriter, e *idemEntry) {
	if e.ctype != "" {
		w.Header().Set("Content-Type", e.ctype)
	}
	w.Header().Set("Idempotency-Replayed", "true")
	w.WriteHeader(e.status)
	_, _ = w.Write(e.body)
}

// gcLocked evicts entries past the TTL. Caller holds s.mu.
func (s *idemStore) gcLocked() {
	cutoff := s.now().Add(-idempotencyTTL)
	for k, e := range s.entries {
		if e.created.Before(cutoff) {
			delete(s.entries, k)
		}
	}
}

// captureWriter tees the response into a buffer so a later replay can
// return identical bytes, while still writing through to the client on
// the first request.
type captureWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.buf.Write(b)
	return c.ResponseWriter.Write(b)
}
