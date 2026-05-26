package handlers

import (
	"context"
	"log"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// BIT-177 — adopt/resync from docker.
//
// The registry's `Status` field is set ONCE per lifecycle event:
// RunUp writes `running` on success or `failed`/`partial` on
// timeout/cancel; RunDown writes `stopped`. After that, nothing
// updates it. Result: the dashboard's status pill can lie for
// hours — e.g. a bring-up that timed out at "wait_healthy" stamps
// `failed`, the user fixes the underlying cause (raises Docker
// memory, restarts splice), the containers go healthy, but the
// registry still says `failed` until the next destructive op.
//
// The container-health panel polls docker live and shows truth,
// but the dashboard row + ActionButton key off the cached status.
//
// This reconciler closes that gap. A background ticker probes
// `docker compose ps` for every registered instance and rewrites
// the registry status when it diverges from docker. The handler
// surfaces are unchanged — they still read the registry — but the
// registry is now eventually-consistent with docker (within one
// reconcileInterval).

// reconcileInterval is how often we sweep every registered
// instance. 15s strikes a balance: fast enough that a user who
// just fixed an OOM and restarted splice sees the dashboard catch
// up within one rotation, slow enough that the per-instance
// `docker compose ps` probes don't peg a laptop CPU. Each probe
// itself is bounded to reconcilePerInstanceTimeout.
const reconcileInterval = 15 * time.Second

// reconcilePerInstanceTimeout caps a single docker probe so a
// stuck daemon can't pin the reconciler goroutine forever. Matches
// the per-instance timeout in handleInstanceContainers (5s — the
// "live" panel uses the same number, so the reconciler's load is
// at most equivalent to one extra Container Health panel polling
// at 1/15 the rate).
const reconcilePerInstanceTimeout = 5 * time.Second

// StartReconciler launches the background sweep. Returns
// immediately; the goroutine runs until ctx is cancelled. Pass
// the same shutdown context the HTTP server uses so a Ctrl-C
// in `dpm localnet ui` stops the reconciler cleanly.
//
// The first sweep happens after one interval — NOT immediately on
// start. Reason: on cold start the registry may have entries from
// a previous run whose containers were torn down out-of-band
// (e.g. `docker compose down` from a terminal). The first sweep
// would correctly flip those to `stopped`, but doing it before
// the operator has any chance to see the dashboard's pre-sweep
// state is confusing — better to give them 15s of "what the
// registry remembers" before reality overrides it.
func StartReconciler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcileAll(ctx)
			}
		}
	}()
}

// reconcileAll iterates every entry in the index and reconciles
// each in serial. Serial (not parallel) because the typical user
// has 1–3 instances, the per-instance probe is 5s worst case, and
// a parallel fan-out would burst N docker subprocesses which is
// worse for laptop CPU than spreading them across the interval.
func reconcileAll(parent context.Context) {
	idx, err := registry.ReadIndex()
	if err != nil {
		// Registry unreadable — log once per sweep and skip. The
		// registry layer's own diagnostics surface the underlying
		// cause (permission, missing file, malformed JSON).
		log.Printf("reconciler: read index: %v", err)
		return
	}
	for _, e := range idx.Entries {
		select {
		case <-parent.Done():
			return
		default:
		}
		ctx, cancel := context.WithTimeout(parent, reconcilePerInstanceTimeout)
		ReconcileOne(ctx, e.Name)
		cancel()
	}
}

// ReconcileOne probes docker for the named instance and writes
// the registry back if the status disagrees. Idempotent; safe to
// call repeatedly. Logs only when an actual flip happens, to keep
// the access log readable.
//
// Returns (oldStatus, newStatus, changed) so callers like the CLI
// `dpm localnet refresh` command can render per-flip output. When
// nothing changed, both old and new are equal and changed is
// false. Probe errors are NOT propagated — a transient docker
// hiccup is treated as "leave stale" rather than flipping noisily;
// the only signal a caller gets is changed=false.
func ReconcileOne(ctx context.Context, name string) (old, neu registry.Status, changed bool) {
	// Skip instances with an in-flight create — the create
	// goroutine owns the status during its lifetime and a
	// reconciler write would race it.
	if jobs.Active(name) {
		return "", "", false
	}
	state, err := registry.Read(name)
	if err != nil {
		// Vanished between ReadIndex and Read — registry deleted
		// or corrupted. Either way nothing to reconcile.
		return "", "", false
	}
	// Transitional states are owned by their respective goroutines
	// (creating = RunUp, stopping = RunDown). The reconciler must
	// not write to them — only the orchestrator knows when the
	// transition is complete.
	switch state.Status {
	case registry.StatusCreating, registry.StatusStopping:
		return state.Status, state.Status, false
	}

	probed, probeErr := containersList(ctx, state.ComposeProject)
	if probeErr != nil {
		// Daemon down, project missing, transient error. Leave
		// the cached status alone — better to render stale than
		// to flip on a transient probe failure.
		return state.Status, state.Status, false
	}

	newStatus := evalStatus(state.Status, probed)
	if newStatus == state.Status {
		return state.Status, state.Status, false
	}

	oldStatus := state.Status
	state.Status = newStatus
	if err := registry.Write(state); err != nil {
		log.Printf("reconciler: write %s status=%s: %v", name, newStatus, err)
		return oldStatus, oldStatus, false
	}
	log.Printf("reconciler: %s status %s → %s (docker truth: %d containers)",
		name, oldStatus, newStatus, len(probed))
	return oldStatus, newStatus, true
}

// evalStatus maps a docker-compose-ps snapshot to the registry
// status the UI should display.
//
// Decision table (containers = docker compose ps output):
//
//	cached=stopped  + no containers      → stopped     (no change)
//	cached=running  + no containers      → stopped     (compose down ran elsewhere)
//	cached=failed   + no containers      → failed      (no change; orchestrator gave up cleanly)
//	cached=partial  + no containers      → failed      (everything's gone; collapse to failed)
//	cached=*        + all healthy/running → running    (the BIT-177 happy path)
//	cached=*        + any restarting/unhealthy/exited → partial
//
// "Healthy" is generous: a container without a HEALTHCHECK in its
// image (e.g. nginx, swagger-ui in Splice's compose) but in state
// `running` is treated as healthy — same convention the UI's
// signalFor uses. Otherwise the reconciler would never call any
// Splice instance `running` because two of the standard
// containers have no healthcheck and would forever drag the
// aggregate into `partial`.
func evalStatus(cached registry.Status, containers []ContainerHealth) registry.Status {
	if len(containers) == 0 {
		// No project / all containers removed. If we thought it
		// was running, demote to stopped — the only way we'd see
		// this is someone ran `docker compose down` directly.
		if cached == registry.StatusRunning {
			return registry.StatusStopped
		}
		// partial with no containers is just failed — there's
		// nothing live to be partial about.
		if cached == registry.StatusPartial {
			return registry.StatusFailed
		}
		return cached
	}

	allHealthy := true
	anyBad := false
	for _, c := range containers {
		switch c.State {
		case "restarting", "dead", "exited", "paused":
			anyBad = true
		}
		switch c.Health {
		case "unhealthy":
			anyBad = true
		case "starting":
			// In-flight; not bad, but not "healthy" yet.
			allHealthy = false
		case "healthy", "":
			// healthy or no-healthcheck-defined — both count
			// toward "healthy enough" iff state == running.
			if c.State != "running" {
				allHealthy = false
			}
		default:
			allHealthy = false
		}
	}

	if anyBad {
		return registry.StatusPartial
	}
	if allHealthy {
		return registry.StatusRunning
	}
	// Mixed: nothing failing, but not everything healthy yet.
	// Most common case: containers up, healthchecks still
	// `starting`. Don't flip a cached `failed` to running until
	// the healthchecks actually go green; keep partial as the
	// "we know docker has it but it's not fully ready" signal.
	return registry.StatusPartial
}
