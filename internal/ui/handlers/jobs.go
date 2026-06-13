package handlers

import (
	"context"
	"sync"
)

// jobRegistry tracks in-flight create-instance goroutines so:
//
//	(1) a second POST /api/instances for a name that's already
//	    being brought up returns 409 instead of racing the first
//	    one to write conflicting state.
//	(2) DELETE /api/instances/{name}/up can resolve the
//	    name to a context.CancelFunc and cancel the goroutine.
//
// Goroutine-safe. Lives at package scope (var jobs *jobRegistry)
// so handler functions don't have to thread it through. Tests
// reset it explicitly via NewJobRegistry; production callers
// receive the package-level instance.
type jobRegistry struct {
	mu   sync.Mutex
	jobs map[string]context.CancelFunc
}

func newJobRegistry() *jobRegistry {
	return &jobRegistry{jobs: make(map[string]context.CancelFunc)}
}

// Register associates the cancel function with the instance name.
// Returns false if a job for `name` is already registered — the
// caller should reject the new POST with 409.
//
// The cancel func is what DELETE invokes; passing it in here
// rather than letting Register call context.WithCancel keeps the
// goroutine ownership clear (the caller's defer cancel() still
// runs on the goroutine's exit path).
func (r *jobRegistry) Register(name string, cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.jobs[name]; exists {
		return false
	}
	r.jobs[name] = cancel
	return true
}

// Unregister removes the entry. The goroutine MUST call this on
// exit (success, failure, or cancellation) — otherwise a future
// POST for the same name fails with 409 forever.
func (r *jobRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, name)
}

// Cancel resolves the name to its cancel func and invokes it.
// Returns false if no job is registered for `name` (the DELETE
// handler maps this to 404). Does NOT remove the entry — the
// goroutine's own defer Unregister runs after the cancel
// propagates.
func (r *jobRegistry) Cancel(name string) bool {
	r.mu.Lock()
	cancel, ok := r.jobs[name]
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// Active returns whether a job is currently registered for the
// given instance name. Used by the POST handler's pre-check
// (cheap fast-fail before any work).
func (r *jobRegistry) Active(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.jobs[name]
	return ok
}

// jobs is the package-level registry. Handlers reference it
// directly. Tests can clear it via jobsReset; in production it
// outlives any single instance.
var jobs = newJobRegistry()

// jobsReset is the test seam to wipe the registry between tests.
// Production callers must NOT use this — it would silently lose
// active cancel funcs.
func jobsReset() {
	jobs = newJobRegistry()
}
