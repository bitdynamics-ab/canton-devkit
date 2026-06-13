package localnet

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func seedMetricsState(name string, cantonPort, splicePort int) *registry.State {
	s := registry.NewState(name, "0.6.4")
	s.Ports = map[string]int{}
	if cantonPort > 0 {
		s.Ports[PortCantonMetrics] = cantonPort
	}
	if splicePort > 0 {
		s.Ports[PortSpliceMetrics] = splicePort
	}
	return s
}

// TestReconcileSharedTargets_DropsOrphanAndDeadStatuses pins the
// orphan-target GC: a target is kept only while its instance is present
// in the registry index AND in a live (or imminently/temporarily live)
// state — running, creating, paused. It is reaped when the instance is
// absent (a crash that left the file behind) or in a dead state. The
// failed/partial cases matter most: a failed or interrupted `down`
// persists StatusFailed/StatusPartial and returns BEFORE deregistering,
// so without reaping them the dead target would be scraped forever and
// pin the refcount so the idle teardown never fires.
func TestReconcileSharedTargets_DropsOrphanAndDeadStatuses(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	port := 30000
	register := func(name string, st registry.Status, inIndex bool) {
		port += 2
		s := seedMetricsState(name, port-1, port)
		if inIndex {
			s.Status = st
			if err := registry.Write(s); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		if err := RegisterInstanceTargets(s); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	keep := []registry.Status{registry.StatusRunning, registry.StatusCreating, registry.StatusPaused}
	reap := []registry.Status{registry.StatusStopped, registry.StatusStopping, registry.StatusFailed, registry.StatusPartial}
	for _, st := range keep {
		register("keep-"+string(st), st, true)
	}
	for _, st := range reap {
		register("reap-"+string(st), st, true)
	}
	register("orphan", "", false) // target file but no index entry

	reconcileSharedTargets()

	for _, st := range keep {
		if !InstanceObservabilityEnabled("keep-" + string(st)) {
			t.Errorf("status %q target was reaped; want kept", st)
		}
	}
	for _, st := range reap {
		if InstanceObservabilityEnabled("reap-" + string(st)) {
			t.Errorf("status %q target was kept; want reaped", st)
		}
	}
	if InstanceObservabilityEnabled("orphan") {
		t.Error("orphan target was kept; want reaped (absent from index)")
	}
	if n := sharedTargetCount(); n != len(keep) {
		t.Errorf("sharedTargetCount = %d, want %d (kept statuses only)", n, len(keep))
	}
}

// TestRegisterInstanceTargets_WritesFileSDShape pins the file_sd JSON the
// shared Prometheus reads: host.docker.internal:<hostport> targets with
// {instance,component} labels so the bundled dashboard's `instance` var
// keeps working.
func TestRegisterInstanceTargets_WritesFileSDShape(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := seedMetricsState("demo", 54101, 54102)

	if err := RegisterInstanceTargets(s); err != nil {
		t.Fatalf("RegisterInstanceTargets: %v", err)
	}
	if !InstanceObservabilityEnabled("demo") {
		t.Fatal("InstanceObservabilityEnabled = false after register")
	}

	body, err := os.ReadFile(sharedTargetPath("demo"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	var groups []fileSDGroup
	if err := json.Unmarshal(body, &groups); err != nil {
		t.Fatalf("target file is not valid file_sd JSON: %v\n%s", err, body)
	}
	if len(groups) != 2 {
		t.Fatalf("want 2 target groups (canton, splice), got %d", len(groups))
	}
	byComp := map[string]fileSDGroup{}
	for _, g := range groups {
		byComp[g.Labels["component"]] = g
	}
	canton, ok := byComp["canton"]
	if !ok {
		t.Fatal("no canton target group")
	}
	if got := canton.Targets[0]; got != "host.docker.internal:54101" {
		t.Errorf("canton target = %q, want host.docker.internal:54101", got)
	}
	if canton.Labels["instance"] != "demo" {
		t.Errorf("canton instance label = %q, want demo", canton.Labels["instance"])
	}
	if _, ok := byComp["splice"]; !ok {
		t.Error("no splice target group")
	}
}

// TestRegisterInstanceTargets_NoPortsFailsLoudly: a bring-up that didn't
// capture the metrics ports must not register an empty (useless) target
// file silently.
func TestRegisterInstanceTargets_NoPortsFailsLoudly(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := seedMetricsState("noports", 0, 0)
	if err := RegisterInstanceTargets(s); err == nil {
		t.Fatal("RegisterInstanceTargets should fail when no metrics ports were captured")
	}
	if InstanceObservabilityEnabled("noports") {
		t.Error("no target file should have been written")
	}
}

// TestDeregisterInstanceTargets_Idempotent
func TestDeregisterInstanceTargets_Idempotent(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	if err := DeregisterInstanceTargets("absent"); err != nil {
		t.Errorf("deregister of absent instance should be a no-op, got %v", err)
	}
	s := seedMetricsState("demo", 1, 2)
	_ = RegisterInstanceTargets(s)
	if err := DeregisterInstanceTargets("demo"); err != nil {
		t.Fatalf("deregister: %v", err)
	}
	if InstanceObservabilityEnabled("demo") {
		t.Error("target file should be gone after deregister")
	}
}

// recordingSharedRunner is a sharedComposeOps stub that records whether
// Up/Stop were called, for the refcount decision tests.
type recordingSharedRunner struct {
	upCalled, stopCalled bool
}

func (r *recordingSharedRunner) Up(context.Context) error { r.upCalled = true; return nil }
func (r *recordingSharedRunner) Stop(context.Context, bool) error {
	r.stopCalled = true
	return nil
}
func (r *recordingSharedRunner) DiscoverPort(context.Context, string, int) (int, error) {
	return 39090, nil
}

// TestTeardownSharedStackIfIdle_RefcountGate: the shared stack is torn
// down ONLY when no instance still references it (no target files). This
// is the refcount that keeps the stack alive while any instance uses it.
func TestTeardownSharedStackIfIdle_RefcountGate(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	rec := &recordingSharedRunner{}
	orig := newSharedRunner
	newSharedRunner = func(string, []string, string, io.Writer) sharedComposeOps { return rec }
	defer func() { newSharedRunner = orig }()

	// Materialise so the compose file exists (teardown short-circuits if not).
	if err := materializeSharedAssets(); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	// Two instances referenced → teardown must be a no-op.
	_ = RegisterInstanceTargets(seedMetricsState("a", 1, 2))
	_ = RegisterInstanceTargets(seedMetricsState("b", 3, 4))
	if err := TeardownSharedStackIfIdle(context.Background(), io.Discard); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if rec.stopCalled {
		t.Error("teardown stopped the shared stack while 2 instances still reference it")
	}

	// Deregister one → still referenced → still no teardown.
	_ = DeregisterInstanceTargets("a")
	rec.stopCalled = false
	_ = TeardownSharedStackIfIdle(context.Background(), io.Discard)
	if rec.stopCalled {
		t.Error("teardown stopped the shared stack while 1 instance still references it")
	}

	// Deregister the last → now idle → teardown fires.
	_ = DeregisterInstanceTargets("b")
	rec.stopCalled = false
	_ = TeardownSharedStackIfIdle(context.Background(), io.Discard)
	if !rec.stopCalled {
		t.Error("teardown did NOT stop the shared stack when no instances reference it")
	}
}

// TestMaterializeSharedAssets_WritesComposeAndConfig
func TestMaterializeSharedAssets_WritesComposeAndConfig(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	if err := materializeSharedAssets(); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	for _, p := range []string{
		sharedComposeFile(),
		sharedPromConfigFile(),
		filepath.Join(sharedGrafanaProvDir(), "datasources"),
		sharedGrafanaDashDir(),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected materialised asset %s: %v", p, err)
		}
	}
	// The shared prometheus config must use file_sd, not the per-instance
	// static canton:10013 scrape.
	body, _ := os.ReadFile(sharedPromConfigFile())
	if !strings.Contains(string(body), "file_sd_configs") {
		t.Error("shared-prometheus.yml should use file_sd_configs")
	}
}

// TestSharedObservabilityRoot_HonorsRegistryOverride keeps the shared
// stack inside the test's temp registry (no escape to the real ~/.canton).
func TestSharedObservabilityRoot_HonorsRegistryOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CANTON_DEVKIT_REGISTRY", dir)
	if got := SharedObservabilityRoot(); !strings.HasPrefix(got, dir) {
		t.Errorf("SharedObservabilityRoot = %q, want under %q", got, dir)
	}
}
