package handlers

import (
	"context"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestCachedCaptureCantonPorts_ServesFromCache pins the BIT-221 #2
// fix: the reconciler hit `docker compose port` 9 times per port
// per cycle (so 9 subprocesses every 15s per running instance). A
// TTL cache eliminates the burst for the common stable case while
// still letting drift propagate within one TTL window.
func TestCachedCaptureCantonPorts_ServesFromCache(t *testing.T) {
	resetCantonPortsCache()
	calls := 0
	origCapture := captureCantonPorts
	t.Cleanup(func() { captureCantonPorts = origCapture })
	captureCantonPorts = func(context.Context, string) map[string]int {
		calls++
		return map[string]int{"participant_admin_app-user": 61252}
	}
	for i := 0; i < 5; i++ {
		got := cachedCaptureCantonPorts(context.Background(), "p")
		if got["participant_admin_app-user"] != 61252 {
			t.Fatalf("iter %d: got %v", i, got)
		}
	}
	if calls != 1 {
		t.Errorf("captureCantonPorts called %d times across 5 cached lookups; want 1", calls)
	}
}

// TestCachedCaptureCantonPorts_EmptyProbeDoesNotPoisonCache asserts
// a probe failure (empty map) is NOT cached — otherwise a single
// flaky docker call would suppress port discovery for the full TTL
// window and consumers would render stale data even after the
// daemon recovers.
func TestCachedCaptureCantonPorts_EmptyProbeDoesNotPoisonCache(t *testing.T) {
	resetCantonPortsCache()
	calls := 0
	origCapture := captureCantonPorts
	t.Cleanup(func() { captureCantonPorts = origCapture })
	captureCantonPorts = func(context.Context, string) map[string]int {
		calls++
		if calls == 1 {
			return nil // first call fails
		}
		return map[string]int{"participant_admin_app-user": 61252}
	}
	// First call: probe failure, not cached.
	if got := cachedCaptureCantonPorts(context.Background(), "p"); len(got) != 0 {
		t.Errorf("first call: want empty (probe failure), got %v", got)
	}
	// Second call: must re-probe (no cached entry from failure).
	got := cachedCaptureCantonPorts(context.Background(), "p")
	if got["participant_admin_app-user"] != 61252 {
		t.Errorf("second call: want fresh probe, got %v", got)
	}
	if calls != 2 {
		t.Errorf("captureCantonPorts called %d times; want 2 (failure must not be cached)", calls)
	}
}

// TestReconcileOne_LockedAgainstConcurrentWrite is the BIT-221 #1
// regression: ReconcileOne used to read state, probe (slow), and
// write the full struct back without re-reading under the lock —
// so any field a concurrent writer (e.g. `localnet down` updating
// Credentials, or the token CLI updating Tokens) flipped while we
// were probing got clobbered.
//
// The fix takes the registry lock, re-reads, and applies ONLY the
// port-drift delta. This test simulates the race by mutating the
// on-disk state.Credentials from inside the captureCantonPorts
// stub — i.e. between our pre-lock snapshot and the lock-acquire
// — then asserts the post-reconcile state still carries the
// "concurrent writer's" credential.
func TestReconcileOne_LockedAgainstConcurrentWrite(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	jobsReset()
	resetCantonPortsCache()

	// Seed: an instance the reconciler thinks is running, with
	// known ports and one credential.
	initial := &registry.State{
		SchemaVersion:  registry.SchemaVersion,
		Name:           "inst",
		Status:         registry.StatusRunning,
		ComposeProject: "canton-inst",
		Ports: map[string]int{
			"participant_admin_app-user": 58953, // stale — will drift
		},
		Credentials: map[string]registry.Credential{
			"app-user": {Role: "app-user", User: "alice", JWT: "OLD"},
		},
	}
	if err := registry.Write(initial); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	// containersList stub: report one healthy container so
	// evalStatus stays at StatusRunning (no flip, only port drift).
	origContainers := reconcileContainersList
	t.Cleanup(func() { reconcileContainersList = origContainers })
	reconcileContainersList = func(context.Context, string) ([]ContainerHealth, error) {
		return []ContainerHealth{{State: "running", Health: "healthy"}}, nil
	}

	// captureCantonPorts stub: simulate a concurrent writer
	// flipping Credentials between our pre-lock state-read and
	// the lock acquisition. If the fix is correct, ReconcileOne
	// re-reads fresh state under the lock and our NEW jwt
	// survives. If the bug is back, ReconcileOne's Write of the
	// stale snapshot clobbers it back to "OLD".
	origCapture := captureCantonPorts
	t.Cleanup(func() { captureCantonPorts = origCapture })
	captureCantonPorts = func(_ context.Context, project string) map[string]int {
		// Concurrent-writer simulation: rewrite Credentials on
		// disk. The writer-side is intentionally lock-free here
		// because the only thing we're testing is that the
		// READER (ReconcileOne) re-reads under its lock.
		racy, err := registry.Read("inst")
		if err != nil {
			t.Errorf("concurrent-writer read: %v", err)
			return nil
		}
		racy.Credentials["app-user"] = registry.Credential{Role: "app-user", User: "alice", JWT: "NEW"}
		if err := registry.Write(racy); err != nil {
			t.Errorf("concurrent-writer write: %v", err)
		}
		// Return a drifted port set so the reconciler has a
		// reason to write at all.
		return map[string]int{"participant_admin_app-user": 61252}
	}

	_, _, _ = ReconcileOne(context.Background(), "inst")

	got, err := registry.Read("inst")
	if err != nil {
		t.Fatalf("post-reconcile read: %v", err)
	}
	if got.Credentials["app-user"].JWT != "NEW" {
		t.Fatalf("ReconcileOne clobbered concurrent writer's Credentials.JWT = %q; want %q",
			got.Credentials["app-user"].JWT, "NEW")
	}
	if got.Ports["participant_admin_app-user"] != 61252 {
		t.Errorf("port drift not applied: got %d, want 61252",
			got.Ports["participant_admin_app-user"])
	}
}

// TestEvalStatus pins the BIT-177 decision table. Each row reads
// like a contract: "given the docker compose ps snapshot, what
// status should the dashboard show?" Failures here would silently
// re-introduce the bug the reconciler exists to fix.
func TestEvalStatus(t *testing.T) {
	healthy := ContainerHealth{State: "running", Health: "healthy"}
	noHealthcheckRunning := ContainerHealth{State: "running"} // nginx, swagger-ui style
	starting := ContainerHealth{State: "running", Health: "starting"}
	unhealthy := ContainerHealth{State: "running", Health: "unhealthy"}
	restarting := ContainerHealth{State: "restarting"}
	exited := ContainerHealth{State: "exited"}

	cases := []struct {
		name       string
		cached     registry.Status
		containers []ContainerHealth
		want       registry.Status
	}{
		{
			name:   "all healthy → running (the BIT-177 happy path)",
			cached: registry.StatusFailed,
			containers: []ContainerHealth{
				healthy, healthy, healthy,
			},
			want: registry.StatusRunning,
		},
		{
			name:       "no-healthcheck container is treated as healthy when running",
			cached:     registry.StatusFailed,
			containers: []ContainerHealth{healthy, noHealthcheckRunning, healthy},
			want:       registry.StatusRunning,
		},
		{
			name:       "any container restarting → partial",
			cached:     registry.StatusRunning,
			containers: []ContainerHealth{healthy, restarting, healthy},
			want:       registry.StatusPartial,
		},
		{
			name:       "any container exited → partial",
			cached:     registry.StatusRunning,
			containers: []ContainerHealth{healthy, exited},
			want:       registry.StatusPartial,
		},
		{
			name:       "any container unhealthy → partial",
			cached:     registry.StatusRunning,
			containers: []ContainerHealth{healthy, unhealthy},
			want:       registry.StatusPartial,
		},
		{
			name:       "mixed healthy + starting (no bad) → partial",
			cached:     registry.StatusFailed,
			containers: []ContainerHealth{healthy, starting},
			want:       registry.StatusPartial,
		},
		{
			name:       "no containers + cached running → stopped (compose down ran elsewhere)",
			cached:     registry.StatusRunning,
			containers: nil,
			want:       registry.StatusStopped,
		},
		{
			name:       "no containers + cached failed → unchanged",
			cached:     registry.StatusFailed,
			containers: nil,
			want:       registry.StatusFailed,
		},
		{
			name:       "no containers + cached stopped → unchanged",
			cached:     registry.StatusStopped,
			containers: nil,
			want:       registry.StatusStopped,
		},
		{
			name:       "no containers + cached partial → failed (nothing to be partial about)",
			cached:     registry.StatusPartial,
			containers: nil,
			want:       registry.StatusFailed,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// nil coreServices = skip the BIT-222 core check; these
			// cases pin the pre-BIT-222 health-averaging behaviour
			// and must keep passing unchanged.
			got := evalStatus(c.cached, c.containers, nil)
			if got != c.want {
				t.Errorf("evalStatus(%v, %d containers) = %v; want %v",
					c.cached, len(c.containers), got, c.want)
			}
		})
	}
}

// TestRefreshCantonPorts pins the BIT-221 fix: when canton has been
// recreated and ephemeral host ports drifted, the reconciler must
// rewrite state.Ports with the live capture, regardless of whether
// the high-level status changed.
//
// The motivating bug: v2-canton restarts (clean exit 0 → docker
// recreates → new ports) while the reconciler still sees the same
// "all-healthy" snapshot before AND after. Status doesn't flip;
// without this fix Explorer / DAR / contracts 502 against the closed
// old port for the entire 15s reconcile interval (or forever if the
// container never goes unhealthy in between).
func TestRefreshCantonPorts(t *testing.T) {
	t.Run("stale canton ports overwritten with live capture", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user":      58953, // stale
				"participant_ledger_app-user":     58955, // stale
				"participant_json_app-user":       58956,
				"app_user_ui":                     65320, // NOT a canton port — must not be touched
				"participant_admin_app-provider":  58954,
				"participant_ledger_app-provider": 58952,
				"participant_json_app-provider":   58957,
				"participant_admin_sv":            58950,
				"participant_ledger_sv":           58949,
				"participant_json_sv":             58951,
			},
		}

		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(_ context.Context, project string) map[string]int {
			if project != "canton-v2" {
				t.Errorf("captureCantonPorts got project %q, want canton-v2", project)
			}
			return map[string]int{
				"participant_admin_app-user":      61252,
				"participant_ledger_app-user":     61247,
				"participant_json_app-user":       61246,
				"participant_admin_app-provider":  61253,
				"participant_ledger_app-provider": 61248,
				"participant_json_app-provider":   61249,
				"participant_admin_sv":            61251,
				"participant_ledger_sv":           61244,
				"participant_json_sv":             61245,
			}
		}

		changed := refreshCantonPorts(context.Background(), state, "v2")
		if !changed {
			t.Fatal("refreshCantonPorts returned false on a drifted port set")
		}
		if state.Ports["participant_admin_app-user"] != 61252 {
			t.Errorf("admin app-user = %d, want 61252", state.Ports["participant_admin_app-user"])
		}
		if state.Ports["participant_ledger_app-user"] != 61247 {
			t.Errorf("ledger app-user = %d, want 61247", state.Ports["participant_ledger_app-user"])
		}
		if state.Ports["app_user_ui"] != 65320 {
			t.Errorf("app_user_ui = %d, want 65320 (must not be touched)", state.Ports["app_user_ui"])
		}
	})

	t.Run("no diff returns false", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user":  61252,
				"participant_ledger_app-user": 61247,
			},
		}
		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(context.Context, string) map[string]int {
			return map[string]int{
				"participant_admin_app-user":  61252,
				"participant_ledger_app-user": 61247,
			}
		}
		if refreshCantonPorts(context.Background(), state, "v2") {
			t.Error("refreshCantonPorts returned true when nothing diffed")
		}
	})

	t.Run("probe failure (empty map) leaves cached ports alone", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user": 99999,
			},
		}
		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(context.Context, string) map[string]int { return nil }
		if refreshCantonPorts(context.Background(), state, "v2") {
			t.Error("refreshCantonPorts returned true on empty live capture")
		}
		if state.Ports["participant_admin_app-user"] != 99999 {
			t.Errorf("port was clobbered to %d on probe failure", state.Ports["participant_admin_app-user"])
		}
	})

	t.Run("partial probe keeps cached for missing keys", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user":  58953,
				"participant_ledger_app-user": 58955,
			},
		}
		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(context.Context, string) map[string]int {
			// Only admin probe succeeded — the contract from
			// canton_ports.go is "missing key = I don't know,
			// leave cached value alone."
			return map[string]int{"participant_admin_app-user": 61252}
		}
		if !refreshCantonPorts(context.Background(), state, "v2") {
			t.Fatal("admin drifted but refresh returned false")
		}
		if state.Ports["participant_admin_app-user"] != 61252 {
			t.Errorf("admin = %d, want 61252", state.Ports["participant_admin_app-user"])
		}
		if state.Ports["participant_ledger_app-user"] != 58955 {
			t.Errorf("ledger clobbered to %d; want cached 58955 to survive", state.Ports["participant_ledger_app-user"])
		}
	})
}

// TestEvalStatus_CoreServices pins BIT-222. The reconciler must
// recognize the zombie state where the core stack (canton + splice +
// postgres + nginx) has been torn down but the `--profile
// observability` sidecars (prometheus, grafana) survived. Before
// this check, evalStatus would happily report `running` because
// every surviving container was healthy.
func TestEvalStatus_CoreServices(t *testing.T) {
	core := []string{"canton", "splice", "postgres", "nginx"}
	healthy := func(svc string) ContainerHealth {
		return ContainerHealth{Service: svc, State: "running", Health: "healthy"}
	}
	noHealthcheck := func(svc string) ContainerHealth {
		return ContainerHealth{Service: svc, State: "running"}
	}
	restarting := func(svc string) ContainerHealth {
		return ContainerHealth{Service: svc, State: "restarting"}
	}

	t.Run("obs zombie (prometheus+grafana only) → failed", func(t *testing.T) {
		got := evalStatus(
			registry.StatusRunning,
			[]ContainerHealth{healthy("prometheus"), healthy("grafana")},
			core,
		)
		if got != registry.StatusFailed {
			t.Errorf("got %v, want failed — sidecar-only stack is not running", got)
		}
	})

	t.Run("core healthy + sidecars present → running (happy path with obs)", func(t *testing.T) {
		got := evalStatus(
			registry.StatusPartial,
			[]ContainerHealth{
				healthy("canton"), healthy("splice"), healthy("postgres"),
				noHealthcheck("nginx"),
				healthy("wallet-web-ui-sv"), healthy("scan-web-ui"),
				healthy("prometheus"), healthy("grafana"),
			},
			core,
		)
		if got != registry.StatusRunning {
			t.Errorf("got %v, want running — full stack with obs sidecars should not regress", got)
		}
	})

	t.Run("core present but canton restarting → partial (no regression)", func(t *testing.T) {
		got := evalStatus(
			registry.StatusRunning,
			[]ContainerHealth{
				restarting("canton"), healthy("splice"), healthy("postgres"),
				noHealthcheck("nginx"),
			},
			core,
		)
		if got != registry.StatusPartial {
			t.Errorf("got %v, want partial — restarting core member is degradation, not zombie", got)
		}
	})

	t.Run("one core service missing → failed", func(t *testing.T) {
		got := evalStatus(
			registry.StatusRunning,
			[]ContainerHealth{
				healthy("canton"), healthy("splice"), healthy("postgres"),
				// nginx is gone — partial stack can't actually serve, treat as failed.
				healthy("wallet-web-ui-sv"),
			},
			core,
		)
		if got != registry.StatusFailed {
			t.Errorf("got %v, want failed — missing core service collapses to failed", got)
		}
	})

	t.Run("nil coreServices preserves old behaviour", func(t *testing.T) {
		got := evalStatus(
			registry.StatusRunning,
			[]ContainerHealth{healthy("prometheus"), healthy("grafana")},
			nil,
		)
		if got != registry.StatusRunning {
			t.Errorf("got %v, want running — nil core list must preserve old averaging", got)
		}
	})

	t.Run("non-core extras don't satisfy the check", func(t *testing.T) {
		got := evalStatus(
			registry.StatusRunning,
			[]ContainerHealth{
				healthy("wallet-web-ui-app-user"),
				healthy("scan-web-ui"),
				healthy("sv-web-ui"),
			},
			core,
		)
		if got != registry.StatusFailed {
			t.Errorf("got %v, want failed — non-core extras don't make a working instance", got)
		}
	})
}
