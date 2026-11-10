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

var captureCantonPorts = localnet.CaptureCantonPorts

var reconcileContainersList = containersList

var coreServicesFor = localnet.CoreServicesFor

var cantonPortsCacheTTL = 14 * time.Second

type cantonPortsCacheEntry struct {
	at    time.Time
	ports map[string]int
}

var (
	cantonPortsCacheMu sync.Mutex
	cantonPortsCache   = map[string]cantonPortsCacheEntry{}
)

func cachedCaptureCantonPorts(ctx context.Context, project string) map[string]int {
	cantonPortsCacheMu.Lock()
	entry, ok := cantonPortsCache[project]
	cantonPortsCacheMu.Unlock()
	if ok && time.Since(entry.at) < cantonPortsCacheTTL {
		return entry.ports
	}
	fresh := captureCantonPorts(ctx, project)
	if len(fresh) == 0 {
		return fresh
	}
	cantonPortsCacheMu.Lock()
	cantonPortsCache[project] = cantonPortsCacheEntry{at: time.Now(), ports: fresh}
	cantonPortsCacheMu.Unlock()
	return fresh
}

func resetCantonPortsCache() {
	cantonPortsCacheMu.Lock()
	cantonPortsCache = map[string]cantonPortsCacheEntry{}
	cantonPortsCacheMu.Unlock()
}

const reconcileInterval = 15 * time.Second

const reconcilePerInstanceTimeout = 5 * time.Second

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

func reconcileAll(parent context.Context) {
	idx, err := registry.ReadIndex()
	if err != nil {
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

func ReconcileOne(ctx context.Context, name string) (old, neu registry.Status, changed bool) {
	if jobs.Active(name) {
		return "", "", false
	}
	state, err := registry.Read(name)
	if err != nil {
		return "", "", false
	}
	switch state.Status {
	case registry.StatusCreating, registry.StatusStopping:
		return state.Status, state.Status, false
	}

	probed, probeErr := reconcileContainersList(ctx, state.ComposeProject)
	if probeErr != nil {
		return state.Status, state.Status, false
	}

	coreServices := coreServicesFor(state.SpliceVersion)
	newStatus := evalStatus(state.Status, probed, coreServices)

	// Gate also includes `partial` — the V2 Token Standard image set
	// has a perpetually-`starting` splice healthcheck that pegs the
	// reconciler at `partial` forever (see assets/compose/tokens-v2.yml).
	var livePorts map[string]int
	if state.ComposeProject != "" &&
		(newStatus == registry.StatusRunning || newStatus == registry.StatusPartial) {
		livePorts = cachedCaptureCantonPorts(ctx, state.ComposeProject)
	}

	if newStatus == state.Status && !portMapDiffers(state.Ports, livePorts) {
		return state.Status, state.Status, false
	}

	release, lockErr := registry.Lock(name)
	if lockErr != nil {
		return state.Status, state.Status, false
	}
	defer release()

	fresh, rerr := registry.Read(name)
	if rerr != nil {
		return state.Status, state.Status, false
	}
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

func refreshCantonPorts(ctx context.Context, state *registry.State, name string) bool {
	live := captureCantonPorts(ctx, state.ComposeProject)
	return applyPortDelta(state, live, name)
}

func evalStatus(cached registry.Status, containers []ContainerHealth, coreServices []string) registry.Status {
	if len(containers) == 0 {
		if cached == registry.StatusRunning {
			return registry.StatusStopped
		}
		if cached == registry.StatusPartial {
			return registry.StatusFailed
		}
		return cached
	}

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

	if cached == registry.StatusStopped {
		anyRunning := false
		for _, c := range containers {
			if c.State == "running" || c.State == "restarting" {
				anyRunning = true
				break
			}
		}
		if !anyRunning {
			return registry.StatusStopped
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
			allHealthy = false
		case "healthy", "":
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
	return registry.StatusPartial
}
