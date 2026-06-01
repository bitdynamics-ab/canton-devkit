package handlers

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// captureCantonPorts is a test seam wrapping localnet.CaptureCantonPorts
// (BIT-221). Tests override this var to feed deterministic port maps
// without shelling out to docker. Production uses the real impl.
var captureCantonPorts = localnet.CaptureCantonPorts

// reconcileContainersList is the test seam wrapping containersList so
// reconciler tests can drive ReconcileOne end-to-end without exec'ing
// docker. Production keeps the real wrapper.
var reconcileContainersList = containersList

// coreServicesFor resolves an instance's core compose service names
// via the splice adapter (BIT-222). Exposed as a var so the
// reconciler tests can swap in a deterministic list without dragging
// the splice catalogue into the test setup. Returns nil when the
// version isn't resolvable; evalStatus treats nil as "skip the core
// check," preserving back-compat with registry entries whose
// splice_version has been renamed or removed.
var coreServicesFor = localnet.CoreServicesFor

// cantonPortsCacheTTL caps how long the reconciler will reuse a
// cached port-capture result. Each `CaptureCantonPorts` call shells
// out to `docker compose port` 9 times — without the cache, the 15s
// reconcile loop would fire 9 docker subprocesses every cycle for
// every running instance, even though host ports only change when
// the canton container is recreated. A TTL slightly shorter than
// the reconcile interval means: cached result for the common
// "everything stable" case (one cycle uses cache from the previous),
// fresh probe at most once per interval. Tests set this to zero via
// resetCantonPortsCache to bypass the cache deterministically.
var cantonPortsCacheTTL = 14 * time.Second

type cantonPortsCacheEntry struct {
	at    time.Time
	ports map[string]int
}

var (
	cantonPortsCacheMu sync.Mutex
	cantonPortsCache   = map[string]cantonPortsCacheEntry{}
)

// cachedCaptureCantonPorts wraps captureCantonPorts with a per-project
// TTL cache. Returns the cached map on a hit; on miss / expiry,
// calls through and stores the fresh result. The cache holds maps
// returned by callers BY VALUE-via-shared-reference — callers must
// treat the returned map as read-only (the reconciler does: it only
// diffs against state.Ports, then merges deltas into the freshly
// re-read state under the registry lock).
func cachedCaptureCantonPorts(ctx context.Context, project string) map[string]int {
	cantonPortsCacheMu.Lock()
	entry, ok := cantonPortsCache[project]
	cantonPortsCacheMu.Unlock()
	if ok && time.Since(entry.at) < cantonPortsCacheTTL {
		return entry.ports
	}
	fresh := captureCantonPorts(ctx, project)
	if len(fresh) == 0 {
		// Don't poison the cache with a probe failure — keep
		// the prior entry (if any) so a transient docker hiccup
		// doesn't force 9 subprocesses on every cycle until the
		// daemon recovers.
		return fresh
	}
	cantonPortsCacheMu.Lock()
	cantonPortsCache[project] = cantonPortsCacheEntry{at: time.Now(), ports: fresh}
	cantonPortsCacheMu.Unlock()
	return fresh
}

// resetCantonPortsCache clears the TTL cache. Exposed for tests that
// need to assert pre/post probe behaviour without waiting out the
// TTL. Not used in production.
func resetCantonPortsCache() {
	cantonPortsCacheMu.Lock()
	cantonPortsCache = map[string]cantonPortsCacheEntry{}
	cantonPortsCacheMu.Unlock()
}

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

	probed, probeErr := reconcileContainersList(ctx, state.ComposeProject)
	if probeErr != nil {
		// Daemon down, project missing, transient error. Leave
		// the cached status alone — better to render stale than
		// to flip on a transient probe failure.
		return state.Status, state.Status, false
	}

	// BIT-222: pull the splice adapter's core-services list so
	// evalStatus can distinguish "core stack missing" (failed) from
	// "core stack running but degraded" (partial). Resolution
	// failures (e.g. an old splice_version no longer in the
	// catalogue) yield nil, which evalStatus interprets as "skip
	// the core check" — preserves back-compat with stale entries.
	coreServices := coreServicesFor(state.SpliceVersion)
	newStatus := evalStatus(state.Status, probed, coreServices)

	// BIT-221: when canton is up (or going up), re-capture the 9
	// participant ports the screens dial. If `canton` was recreated
	// since the last bring-up (clean restart, OOM restart, etc.)
	// docker reassigned its ephemeral host ports — but BIT-190's
	// capture-on-WaitForHealthy never gets a second chance to update
	// state.Ports. Without this the Explorer / DAR / contracts
	// handlers happily 502 against a port that's been closed for
	// minutes while the reconciler reports `running` or `partial`.
	//
	// Refresh BEFORE the status-changed early-return so the common
	// case (cached=running, newStatus=running, only ports drifted)
	// gets the fix. Gate also includes `partial` because the V2
	// Token Standard image set has a perpetually-`starting` splice
	// healthcheck (see assets/compose/tokens-v2.yml) — those are the
	// instances where canton most often restart-loops AND the
	// screens most need a working port set; gating on `running`
	// alone would never fire for them.
	// `refreshCantonPorts` itself is best-effort — missing probes
	// leave cached values intact — so calling it during `partial`
	// is safe (probe failure ⇒ no diff ⇒ no write).
	// Probe ports OUTSIDE the registry lock — the docker subprocesses
	// can take seconds, and we don't want to block concurrent writers
	// (e.g. `localnet down`) on a slow daemon. We only diff against
	// state.Ports here; the authoritative apply-and-write happens
	// below under the lock against a freshly-re-read state.
	var livePorts map[string]int
	if state.ComposeProject != "" &&
		(newStatus == registry.StatusRunning || newStatus == registry.StatusPartial) {
		livePorts = cachedCaptureCantonPorts(ctx, state.ComposeProject)
	}

	if newStatus == state.Status && !portMapDiffers(state.Ports, livePorts) {
		return state.Status, state.Status, false
	}

	// Acquire the registry lock and re-read fresh state before
	// applying changes. Mirrors the lock+re-read pattern in
	// internal/cli/localnet/down.go — without it a concurrent
	// `localnet down` (or a future Web UI POST) could clobber our
	// stale snapshot's Credentials / DSO / Tokens fields when we
	// Write(state) here.
	release, lockErr := registry.Lock(name)
	if lockErr != nil {
		// Lock contention or fs error — treat like a probe failure
		// and leave the cached status alone for this cycle.
		return state.Status, state.Status, false
	}
	defer release()

	fresh, rerr := registry.Read(name)
	if rerr != nil {
		// Vanished between probe and lock — nothing to write.
		return state.Status, state.Status, false
	}
	// Re-evaluate transitional states after re-read: a concurrent
	// `down` may have flipped to stopping while we were probing.
	switch fresh.Status {
	case registry.StatusCreating, registry.StatusStopping:
		return fresh.Status, fresh.Status, false
	}

	oldStatus := fresh.Status
	portsChanged := applyPortDelta(fresh, livePorts, name)
	statusChanged := newStatus != fresh.Status
	if statusChanged {
		fresh.Status = newStatus
	}
	if !statusChanged && !portsChanged {
		return oldStatus, oldStatus, false
	}
	if err := registry.Write(fresh); err != nil {
		log.Printf("reconciler: write %s status=%s: %v", name, newStatus, err)
		return oldStatus, oldStatus, false
	}
	if statusChanged {
		log.Printf("reconciler: %s status %s → %s (docker truth: %d containers)",
			name, oldStatus, newStatus, len(probed))
	}
	return oldStatus, fresh.Status, statusChanged || portsChanged
}

// portMapDiffers reports whether `live` contains any port that
// disagrees with `cached`. A missing key in `live` is "I don't know"
// (per CaptureCantonPorts contract) and does NOT count as a diff.
// Returns false when live is empty (probe failure / not gated).
func portMapDiffers(cached, live map[string]int) bool {
	if len(live) == 0 {
		return false
	}
	for k, v := range live {
		if cached[k] != v {
			return true
		}
	}
	return false
}

// applyPortDelta writes only the changed keys from `live` into
// fresh.Ports, preserving every other key (including non-canton
// ports like app_user_ui). Returns true when at least one port
// changed. Mirrors the original refreshCantonPorts semantics but
// operates on the freshly-re-read state held under the registry
// lock — so a concurrent down's Credentials/DSO/Tokens writes are
// preserved rather than clobbered by our stale snapshot.
func applyPortDelta(fresh *registry.State, live map[string]int, name string) bool {
	if len(live) == 0 {
		return false
	}
	if fresh.Ports == nil {
		fresh.Ports = make(map[string]int, len(live))
	}
	changedKeys := make([]string, 0, 4)
	for key, port := range live {
		if fresh.Ports[key] != port {
			changedKeys = append(changedKeys, key)
			fresh.Ports[key] = port
		}
	}
	if len(changedKeys) == 0 {
		return false
	}
	sort.Strings(changedKeys)
	log.Printf("reconciler: %s canton ports drifted: %v", name, changedKeys)
	return true
}

// refreshCantonPorts probes `docker compose port` for the 9 canonical
// canton participant ports and updates state.Ports in place with any
// diff. Returns true when at least one port changed.
//
// Probe failures are tolerated — `localnet.CaptureCantonPorts` is
// best-effort (a port that fails to query is OMITTED from the result
// rather than returned as 0). Missing keys leave the cached value
// alone, matching that contract — better to render stale than to
// blank out ports on a transient docker hiccup.
//
// NOTE: ReconcileOne no longer calls this — it splits the probe and
// the in-place write so the write can happen under the registry
// lock against a freshly-re-read state. The function survives as a
// stable unit-test seam for the in-place diff/merge logic.
func refreshCantonPorts(ctx context.Context, state *registry.State, name string) bool {
	live := captureCantonPorts(ctx, state.ComposeProject)
	return applyPortDelta(state, live, name)
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
//	any core service missing from snapshot → failed     (BIT-222; e.g. obs-only zombie)
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
//
// coreServices is the BIT-222 contract from splice.Adapter: the
// compose service names whose absence collapses the snapshot to
// `failed` regardless of how the surviving containers look. Pass
// nil to skip the core check (back-compat for callers that don't
// know the adapter, e.g. older registry entries with an
// unresolvable splice_version).
func evalStatus(cached registry.Status, containers []ContainerHealth, coreServices []string) registry.Status {
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

	// BIT-222: distinguish "core stack missing" from "core stack
	// running but degraded". If even one core service has no
	// container in the snapshot, the instance can't serve a
	// Ledger API call regardless of how the sidecars look. The
	// motivating bug: an instance whose core stack was torn down
	// but whose `--profile observability` containers (prometheus
	// + grafana) survived would happily report `running` because
	// the surviving sidecars were all healthy.
	if len(coreServices) > 0 {
		seen := make(map[string]bool, len(containers))
		for _, c := range containers {
			seen[c.Service] = true
		}
		for _, svc := range coreServices {
			if !seen[svc] {
				return registry.StatusFailed
			}
		}
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
