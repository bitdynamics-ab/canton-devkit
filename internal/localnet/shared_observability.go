package localnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/assets"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// Shared, host-level observability stack (Dev Fund #39). ONE Prometheus +
// Grafana serves every running LocalNet instead of a per-instance pair.
// The stack runs as its own compose project, independent of any instance
// lifecycle, and scrapes each instance's 127.0.0.1-published canton/splice
// :10013 metrics ports via host.docker.internal, discovered through a
// per-instance Prometheus file_sd target file. Refcount = number of target
// files; the stack starts on the first register and is torn down when the
// last instance deregisters.
//
// What is unit-tested here: the target-file lifecycle, the refcount
// decision, the file_sd JSON shape, and path derivation. What requires a
// live Docker host (see docs/observability.md): the actual scrape
// reachability and the Linux host.docker.internal:host-gateway mapping.

// SharedObservabilityProject is the fixed compose project name for the
// single host-level stack.
const SharedObservabilityProject = "canton-devkit-observability"

const (
	// grafanaDashboardUID pins the bundled Canton LocalNet dashboard UID
	// (assets/grafana/dashboards/canton-localnet.json). Mirrors the const
	// in internal/ui/handlers; both build /d/<uid> deep links.
	grafanaDashboardUID = "canton-localnet-v1"

	// PortCantonMetrics / PortSpliceMetrics are the state.Ports keys for
	// the 127.0.0.1-published canton/splice :10013 metrics ports the
	// shared Prometheus scrapes (captured at `up` — see canton_ports.go).
	PortCantonMetrics = "canton_metrics"
	PortSpliceMetrics = "splice_metrics"
)

// ErrSharedStackNotRunning is returned by the endpoint resolvers when the
// shared stack isn't up (observability off for every instance).
var ErrSharedStackNotRunning = errors.New("shared observability stack is not running")

// sharedComposeOps is the subset of *docker.ComposeRunner the shared-stack
// lifecycle drives. Extracted so tests can assert the refcount/ensure/
// teardown decisions without touching Docker.
type sharedComposeOps interface {
	Up(ctx context.Context) error
	Stop(ctx context.Context, removeVolumes bool) error
	DiscoverPort(ctx context.Context, service string, containerPort int) (int, error)
}

// newSharedRunner builds the production runner for the shared stack. A
// test seam (package var) so unit tests inject a stub.
var newSharedRunner = func(composeFile string, env []string, workDir string, logw io.Writer) sharedComposeOps {
	return &docker.ComposeRunner{
		ProjectName:  SharedObservabilityProject,
		ComposeFiles: []string{composeFile},
		Env:          env,
		WorkDir:      workDir,
		LogWriter:    logw,
	}
}

// SharedObservabilityRoot is the on-disk home of the shared stack:
// <registry-root>/_observability (honours CANTON_DEVKIT_REGISTRY via
// registry.Root(), so tests are isolated). Holds the materialised compose
// + config and the file_sd targets dir. The leading underscore can't
// collide with an instance dir — instance names never start with one.
func SharedObservabilityRoot() string {
	return filepath.Join(registry.Root(), "_observability")
}

func sharedTargetsDir() string { return filepath.Join(SharedObservabilityRoot(), "targets") }
func sharedComposeFile() string {
	return filepath.Join(SharedObservabilityRoot(), "shared-observability.yaml")
}
func sharedPromConfigFile() string {
	return filepath.Join(SharedObservabilityRoot(), "shared-prometheus.yml")
}
func sharedGrafanaProvDir() string {
	return filepath.Join(SharedObservabilityRoot(), "grafana", "provisioning")
}
func sharedGrafanaDashDir() string {
	return filepath.Join(SharedObservabilityRoot(), "grafana", "dashboards")
}
func sharedTargetPath(instance string) string {
	return filepath.Join(sharedTargetsDir(), instance+".json")
}

// fileSDGroup is one Prometheus file_sd target group (a {targets,labels}
// entry in the JSON array file_sd_configs reads).
type fileSDGroup struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// targetGroupsFor builds the file_sd groups for an instance from its
// captured canton/splice :10013 host ports. Each target is
// host.docker.internal:<hostport> labelled with the instance + component,
// so the bundled dashboard's `instance` template var keeps working.
func targetGroupsFor(state *registry.State) []fileSDGroup {
	var groups []fileSDGroup
	add := func(component string, port int) {
		if port <= 0 {
			return
		}
		groups = append(groups, fileSDGroup{
			Targets: []string{fmt.Sprintf("host.docker.internal:%d", port)},
			// No "job" label: file_sd's job would override the scrape
			// config's job_name (localnet) in shared-prometheus.yml. The
			// dashboard filters on {instance,component}, so component is
			// already its own label — leave job=localnet from the config.
			Labels: map[string]string{"instance": state.Name, "component": component},
		})
	}
	add("canton", state.Ports[PortCantonMetrics])
	add("splice", state.Ports[PortSpliceMetrics])
	return groups
}

// RegisterInstanceTargets writes <root>/targets/<name>.json from the
// instance's captured metrics host ports. Idempotent (overwrites). The
// caller then calls EnsureSharedStack. Returns an error if the instance
// has no metrics ports to scrape (so a bring-up that didn't capture them
// fails loudly rather than registering an empty target file).
func RegisterInstanceTargets(state *registry.State) error {
	groups := targetGroupsFor(state)
	if len(groups) == 0 {
		return fmt.Errorf("instance %q has no captured metrics ports (%s/%s) to scrape",
			state.Name, PortCantonMetrics, PortSpliceMetrics)
	}
	if err := os.MkdirAll(sharedTargetsDir(), 0o755); err != nil {
		return fmt.Errorf("create shared targets dir: %w", err)
	}
	body, err := json.MarshalIndent(groups, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal file_sd targets: %w", err)
	}
	return writeFileAtomic(sharedTargetPath(state.Name), append(body, '\n'))
}

// DeregisterInstanceTargets removes the instance's target file. Idempotent
// — no error when it's already gone. The caller then calls
// TeardownSharedStackIfIdle.
func DeregisterInstanceTargets(instance string) error {
	if err := os.Remove(sharedTargetPath(instance)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RegisterInstanceAndEnsureStack registers the instance's scrape targets
// and (re)starts the shared stack as ONE step under the shared-stack lock
// (lockSharedStack), so a concurrent `down` of the last other instance
// can't observe a zero refcount and tear the stack down between this
// instance's register and ensure — which would leave it registered
// against a stopped stack. It also reconciles away target files orphaned
// by a crash before counting/ensuring. Best-effort at the call site: a
// returned error is surfaced as a warning, never a failed bring-up.
func RegisterInstanceAndEnsureStack(ctx context.Context, state *registry.State, logw io.Writer) (SharedStackPorts, error) {
	release, err := lockSharedStack()
	if err != nil {
		return SharedStackPorts{}, err
	}
	defer release()
	if err := RegisterInstanceTargets(state); err != nil {
		return SharedStackPorts{}, err
	}
	reconcileSharedTargets()
	return EnsureSharedStack(ctx, logw)
}

// DeregisterInstanceAndTeardownIfIdle removes the instance's scrape
// targets and tears the shared stack down if no instance still references
// it — as ONE step under the shared-stack lock, the teardown counterpart
// to RegisterInstanceAndEnsureStack. Reconciles orphaned target files
// first so a crashed instance's stale file can't pin the stack alive.
func DeregisterInstanceAndTeardownIfIdle(ctx context.Context, instance string, logw io.Writer) error {
	release, err := lockSharedStack()
	if err != nil {
		return err
	}
	defer release()
	if err := DeregisterInstanceTargets(instance); err != nil {
		return err
	}
	reconcileSharedTargets()
	return TeardownSharedStackIfIdle(ctx, logw)
}

// reconcileSharedTargets drops target files orphaned by a crash or a
// removed instance: any targets/<name>.json whose instance is absent from
// the registry index, or recorded stopped, is removed. Otherwise the
// shared Prometheus keeps scraping a dead host.docker.internal:<port> and
// sharedTargetCount() stays inflated so the idle teardown never fires.
// Best-effort and called only under the shared-stack lock.
func reconcileSharedTargets() {
	entries, err := os.ReadDir(sharedTargetsDir())
	if err != nil {
		return
	}
	idx, err := registry.ReadIndex()
	if err != nil {
		return
	}
	status := make(map[string]registry.Status, len(idx.Entries))
	for _, e := range idx.Entries {
		status[e.Name] = e.Status
	}
	for _, ent := range entries {
		if ent.IsDir() || filepath.Ext(ent.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(ent.Name(), ".json")
		st, known := status[name]
		// Reap a target whose instance is gone from the index, or is in a
		// state with no live :10013 to scrape. A failed or interrupted
		// `down` persists StatusFailed/StatusPartial and returns BEFORE the
		// deregister call (down.go), so without reaping these the dead
		// host.docker.internal:<port> would be scraped forever and the
		// refcount would stay pinned so the idle teardown never fires.
		// Keep running/creating (live or imminently live) and paused (the
		// containers exist and resume; the failing scrape while frozen is
		// cosmetic) — and keep any unknown future status, since wrongly
		// reaping a LIVE instance's target loses its metrics until the next
		// up re-registers, the worse failure direction.
		if !known || sharedTargetDeadStatus(st) {
			_ = os.Remove(filepath.Join(sharedTargetsDir(), ent.Name()))
		}
	}
}

// sharedTargetDeadStatus reports whether an instance status means its
// scrape target should be reaped (no live metrics endpoint): stopped or
// mid-stop, or the failed/partial states a failed/interrupted teardown
// leaves behind. Running/creating/paused (and any unrecognised status)
// are kept — see reconcileSharedTargets for why the keep-on-unknown
// default is the safe one.
func sharedTargetDeadStatus(st registry.Status) bool {
	switch st {
	case registry.StatusStopped, registry.StatusStopping,
		registry.StatusFailed, registry.StatusPartial:
		return true
	default:
		return false
	}
}

// InstanceObservabilityEnabled reports whether the instance is registered
// with the shared stack (its target file exists). This is the signal the
// CLI/UI status and ReadObservabilityState use now that observability is
// no longer a per-instance container set.
func InstanceObservabilityEnabled(instance string) bool {
	_, err := os.Stat(sharedTargetPath(instance))
	return err == nil
}

// sharedTargetCount is the refcount: how many instances reference the
// shared stack. The filesystem is the source of truth (no separate
// counter to desync).
func sharedTargetCount() int {
	entries, err := os.ReadDir(sharedTargetsDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			n++
		}
	}
	return n
}

// SharedStackPorts is the resolved host:port of the shared stack's
// Prometheus and Grafana.
type SharedStackPorts struct {
	PrometheusPort int
	GrafanaPort    int
}

// EnsureSharedStack materialises the shared compose project + assets and
// starts it (idempotent — a no-op `up -d` when already running), then
// returns its discovered host ports. Host ports are ephemeral (compose
// binds 127.0.0.1:0) so the shared stack never collides with an
// instance's own published ports.
func EnsureSharedStack(ctx context.Context, logw io.Writer) (SharedStackPorts, error) {
	if err := materializeSharedAssets(); err != nil {
		return SharedStackPorts{}, err
	}
	runner := newSharedRunner(sharedComposeFile(), sharedStackEnv(), SharedObservabilityRoot(), logw)
	if err := runner.Up(ctx); err != nil {
		return SharedStackPorts{}, fmt.Errorf("start shared observability stack: %w", err)
	}
	return discoverSharedPorts(ctx, runner)
}

// TeardownSharedStackIfIdle stops + removes the shared stack ONLY when no
// instance still references it (no target files remain). No-op otherwise.
// removeVolumes=true: the shared Prometheus TSDB is disposable dev metrics,
// not ledger state.
func TeardownSharedStackIfIdle(ctx context.Context, logw io.Writer) error {
	if sharedTargetCount() > 0 {
		return nil // still referenced
	}
	if _, err := os.Stat(sharedComposeFile()); os.IsNotExist(err) {
		return nil // never materialised
	}
	runner := newSharedRunner(sharedComposeFile(), sharedStackEnv(), SharedObservabilityRoot(), logw)
	return runner.Stop(ctx, true)
}

// SharedPrometheusEndpoint resolves the running shared Prometheus
// host:port, or ErrSharedStackNotRunning when the stack is down.
func SharedPrometheusEndpoint(ctx context.Context) (string, int, error) {
	runner := newSharedRunner(sharedComposeFile(), sharedStackEnv(), SharedObservabilityRoot(), io.Discard)
	port, err := runner.DiscoverPort(ctx, "prometheus", 9090)
	if err != nil || port == 0 {
		return "", 0, ErrSharedStackNotRunning
	}
	return "127.0.0.1", port, nil
}

// SharedGrafanaURL builds the deep link to the bundled dashboard on the
// shared Grafana, pre-filtered to one instance (?var-instance=<name>).
// Returns "" when the stack isn't running.
func SharedGrafanaURL(ctx context.Context, instance string) string {
	runner := newSharedRunner(sharedComposeFile(), sharedStackEnv(), SharedObservabilityRoot(), io.Discard)
	port, err := runner.DiscoverPort(ctx, "grafana", 3000)
	if err != nil || port == 0 {
		return ""
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/d/%s", port, grafanaDashboardUID)
	if instance != "" {
		url += "?var-instance=" + instance
	}
	return url
}

func discoverSharedPorts(ctx context.Context, runner sharedComposeOps) (SharedStackPorts, error) {
	prom, err := runner.DiscoverPort(ctx, "prometheus", 9090)
	if err != nil {
		return SharedStackPorts{}, fmt.Errorf("discover shared prometheus port: %w", err)
	}
	graf, _ := runner.DiscoverPort(ctx, "grafana", 3000) // grafana optional
	return SharedStackPorts{PrometheusPort: prom, GrafanaPort: graf}, nil
}

// sharedStackEnv is the compose interpolation env for the shared stack:
// absolute mount paths for the config/targets/grafana assets, and
// ephemeral host ports (0) so the kernel assigns free ones.
func sharedStackEnv() []string {
	return []string{
		"PROM_CONFIG_FILE=" + sharedPromConfigFile(),
		"PROM_TARGETS_DIR=" + sharedTargetsDir(),
		"GRAFANA_PROVISIONING_DIR=" + sharedGrafanaProvDir(),
		"GRAFANA_DASHBOARDS_DIR=" + sharedGrafanaDashDir(),
		"PROMETHEUS_HOST_PORT=0",
		"GRAFANA_HOST_PORT=0",
	}
}

// materializeSharedAssets writes the shared compose file, prometheus
// config, and the grafana provisioning + dashboards from the embedded FS
// into SharedObservabilityRoot(), plus an empty targets dir. Idempotent.
func materializeSharedAssets() error {
	root := SharedObservabilityRoot()
	if err := os.MkdirAll(sharedTargetsDir(), 0o755); err != nil {
		return fmt.Errorf("create shared observability root: %w", err)
	}
	// compose + prometheus config (flattened to the root).
	for src, dest := range map[string]string{
		"compose/shared-observability.yaml": sharedComposeFile(),
		"compose/shared-prometheus.yml":     sharedPromConfigFile(),
	} {
		data, err := fs.ReadFile(assets.FS, src)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", src, err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
	}
	// grafana/ tree (provisioning + dashboards), preserving layout under root.
	return fs.WalkDir(assets.FS, "grafana", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(root, path), 0o755)
		}
		data, err := fs.ReadFile(assets.FS, path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, path), data, 0o644)
	})
}

// writeFileAtomic writes data via a temp file + rename so a concurrent
// Prometheus file_sd read never sees a half-written target file.
func writeFileAtomic(dest string, data []byte) error {
	tmp := dest + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
