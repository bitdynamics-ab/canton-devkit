package docker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// Splice LocalNet's onboarding flow (DAML package vetting, SV keygen,
	// validator registration) routinely takes 3–5 minutes on a cold start,
	// and 18+ minutes has been observed on a resource-constrained machine
	// with no cached images. 25 minutes gives realistic headroom without
	// leaking goroutines indefinitely on a genuinely stuck daemon; the
	// caller can still ^C earlier.
	readinessTimeout  = 25 * time.Minute
	readinessPollWait = 3 * time.Second
)

type ComposeRunner struct {
	ProjectName string
	// ComposeFiles is the ordered list of `-f` files passed to docker
	// compose. Later files override earlier ones.
	ComposeFiles []string
	// EnvFiles is the ordered list of --env-file paths. Compose
	// interpolates variables across these files (and the shell env), but
	// values are loaded literally — for cross-file `${VAR:-default}`
	// expansion the shell env must be primed before docker compose runs.
	EnvFiles []string
	// Env, when non-nil, replaces the inherited process environment for
	// every `docker compose` invocation. WorkDir is the cwd for every
	// invocation. Both must be set whenever ComposeFiles/EnvFiles use
	// relative paths, which they always do for the cached Splice project.
	Env       []string
	WorkDir   string
	LogWriter io.Writer
	// Profiles, when non-empty, becomes one or more `--profile P`
	// args on every compose invocation. Compose services scoped
	// under a profile via `profiles: [P]` are skipped unless that
	// profile is enabled — used by the `observability` overlay
	// to opt users into the Prometheus + Grafana stack
	// without forcing the extra ~600 MiB on the default path.
	Profiles []string

	// commandFn is the seam tests use to inject a fake docker. Production
	// callers leave it nil; tests set it to capture (and optionally script
	// the output of) every `docker compose ...` invocation.
	commandFn func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// command constructs the *exec.Cmd for one `docker ...` invocation with
// Dir + Env applied. Every method in this file routes through here so
// no call site can accidentally drop WorkDir or Env.
func (c *ComposeRunner) command(ctx context.Context, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if c.commandFn != nil {
		cmd = c.commandFn(ctx, "docker", args...)
	} else {
		cmd = exec.CommandContext(ctx, "docker", args...)
	}
	cmd.Dir = c.WorkDir
	if c.Env != nil {
		cmd.Env = c.Env
	}
	return cmd
}

// workDirOrFallback returns dir when it exists, otherwise a neutral temp
// dir. The label-only teardown (`compose -p <project> down
// --remove-orphans`) and RemainingContainers don't need the project's
// compose files, so falling back keeps them working even after the
// shared Splice cache dir (the recorded WorkDir) has been pruned. Without
// the fallback the chdir fails, `down` errors, and `clean` could then
// orphan the containers by scrubbing their registry entry.
func workDirOrFallback(dir string) string {
	if dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return os.TempDir()
}

// RemainingContainers returns the ids of every container (running or
// stopped) still labelled for this compose project. `clean` calls it to
// confirm a teardown actually cleared docker BEFORE it scrubs the
// registry — so a `down` that errored or silently no-op'd can't orphan
// containers by deleting their only record. It shells out to plain
// `docker ps` with the project-label filter, independent of any compose
// file, so it works even after the shared Splice cache dir is gone.
func (c *ComposeRunner) RemainingContainers(ctx context.Context) ([]string, error) {
	cmd := c.command(ctx, "ps", "-aq",
		"--filter", "label=com.docker.compose.project="+c.ProjectName)
	cmd.Dir = workDirOrFallback(c.WorkDir)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps for project %q: %w", c.ProjectName, err)
	}
	return strings.Fields(string(out)), nil
}

// composeBase returns the leading docker-compose argv shared by Up/Down/
// ps/etc.
func (c *ComposeRunner) composeBase() []string {
	args := []string{"compose", "-p", c.ProjectName}
	for _, f := range c.ComposeFiles {
		args = append(args, "-f", f)
	}
	for _, ef := range c.EnvFiles {
		args = append(args, "--env-file", ef)
	}
	for _, p := range c.Profiles {
		// `--profile` is per-invocation; emitting it here keeps the
		// enabled profile set identical across `up` / `ps` / etc.
		args = append(args, "--profile", p)
	}
	return args
}

func (c *ComposeRunner) Up(ctx context.Context) error {
	// --wait is omitted deliberately: compose's --wait surfaces transient
	// "unhealthy" states as fatal during long Splice onboarding, so
	// WaitForHealthy does its own polling with a Splice-sized timeout.
	// Compose still honors `depends_on: { condition: service_healthy }`
	// during `up -d`; the overlay from
	// internal/localnet.WriteContainerRenameOverlay downgrades Splice's
	// nginx→splice condition to service_started so onboarding (which
	// exceeds the healthcheck retry budget) can't time out that wait.
	args := append(c.composeBase(), "up", "-d")

	cmd := c.command(ctx, args...)
	cmd.Stdout = c.LogWriter
	cmd.Stderr = c.LogWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	return nil
}

func (c *ComposeRunner) WaitForHealthy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()

	// Keep the last raw `ps` snapshot so the timeout error can report
	// which services were stuck and in what state. The most common
	// stuck-state is splice in `restarting/starting` due to OOM-kill
	// from insufficient Docker memory (see docs/limitations.md).
	var lastSnapshot []byte
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for services to become healthy.\nLast `docker compose ps` snapshot:\n%s", formatHealthSnapshot(lastSnapshot))
		default:
		}

		raw, ready, fatal := c.healthSnapshotRaw(ctx)
		if raw != nil {
			lastSnapshot = raw
		}
		if fatal != nil {
			return fatal
		}
		if ready {
			return nil
		}

		// Out-of-band readyz fallback for the V2 alpha track: the
		// upstream `-dev` splice image's in-container HEALTHCHECK probe
		// is broken (its URL never resolves), so docker-reported health
		// stays `starting` forever even after the validator's
		// `/api/validator/readyz` returns 200. When the only non-ready
		// services are splice-shaped and in running/starting, probe
		// readyz via `docker exec` and treat a 200 as ready. Stable
		// splice (0.6.4) is unaffected — its healthcheck works, so the
		// snapshot flips healthy before this fallback runs.
		if c.allBlockersAreSpliceStarting(lastSnapshot) && c.spliceReadyzOK(ctx, lastSnapshot) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for services to become healthy.\nLast `docker compose ps` snapshot:\n%s", formatHealthSnapshot(lastSnapshot))
		case <-time.After(readinessPollWait):
		}
	}
}

// allBlockersAreSpliceStarting returns true iff every non-ready service
// in the snapshot is a `*-splice`-named container in state=running and
// health=starting. The narrow gate keeps the readyz fallback from
// masking real failures in other services (canton, postgres, nginx).
func (c *ComposeRunner) allBlockersAreSpliceStarting(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	sawSplice := false
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return false
		}
		name, state, health := parts[0], parts[1], strings.TrimSpace(parts[2])
		// Ready-bucket entries don't block — skip.
		if state == "running" && (health == "healthy" || health == "") {
			continue
		}
		// Anything else must be splice/running/starting.
		if state != "running" || health != "starting" || !strings.HasSuffix(name, "-splice") {
			return false
		}
		sawSplice = true
	}
	return sawSplice
}

// spliceReadyzOK execs into the splice container and probes
// `http://localhost:2903/api/validator/readyz`; a 200 means ready.
// Best-effort: any error (curl missing, network, non-200) → false,
// which simply means "keep polling".
func (c *ComposeRunner) spliceReadyzOK(ctx context.Context, raw []byte) bool {
	name := spliceContainerName(raw)
	if name == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// curl is present in the splice image (verified against the
	// 0.6.5-snapshot `-dev` image). -sf keeps stdout empty and
	// returns non-zero on HTTP errors, so a successful exit means 200.
	cmd := c.command(probeCtx,
		"exec", name,
		"curl", "-sf", "-o", "/dev/null",
		"http://localhost:2903/api/validator/readyz")
	return cmd.Run() == nil
}

// spliceContainerName extracts the first `*-splice` container name from
// the snapshot. Returns "" when none present.
func spliceContainerName(raw []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		if strings.HasSuffix(parts[0], "-splice") {
			return parts[0]
		}
	}
	return ""
}

// formatHealthSnapshot turns the raw tab-separated `docker compose ps`
// output into a tidy table for the timeout error message.
func formatHealthSnapshot(raw []byte) string {
	if len(raw) == 0 {
		return "  (no services reported)"
	}
	var b strings.Builder
	b.WriteString("  NAME                              STATE        HEALTH\n")
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		fmt.Fprintf(&b, "  %-32s  %-11s  %s\n",
			parts[0], parts[1], strings.TrimSpace(parts[2]))
	}
	return b.String()
}

// healthSnapshot runs `docker compose ps` once and classifies the result.
// ready=true means every service is in a terminal-good state and the
// poller can stop; fatal!=nil means something is irrecoverably broken
// (e.g. a service exited non-zero) and the caller should fail fast
// instead of polling further.
//
// Empty output (no services at all) is treated as not-ready rather than
// fatal: compose may not have registered the project yet right after
// `up` returns.
func (c *ComposeRunner) healthSnapshot(ctx context.Context) (ready bool, fatal error) {
	_, ready, fatal = c.healthSnapshotRaw(ctx)
	return ready, fatal
}

// healthSnapshotRaw is the variant the WaitForHealthy loop uses so it
// can stash the raw ps output for inclusion in a timeout error.
func (c *ComposeRunner) healthSnapshotRaw(ctx context.Context) (raw []byte, ready bool, fatal error) {
	// Tab-separated to keep parsing trivial even with whitespace in
	// future fields. Order: name, state, health.
	args := append(c.composeBase(), "ps", "--all", "--format", "{{.Name}}\t{{.State}}\t{{.Health}}")
	out, err := c.command(ctx, args...).Output()
	if err != nil {
		// Transient errors (e.g. daemon momentarily unavailable) shouldn't
		// abort the whole wait — keep polling.
		return nil, false, nil
	}
	ready, fatal = classifyHealth(out)
	return out, ready, fatal
}

// classifyHealth is the pure parser behind healthSnapshot. Extracted so
// the readiness logic can be exercised by unit tests against canned
// `docker compose ps` output without invoking docker.
//
// Decision table (per service):
//
//	state=running, health=healthy       → counts toward ready
//	state=running, health=starting      → not ready, keep polling
//	state=running, health=unhealthy     → not ready, keep polling
//	state=running, health=""            → counts toward ready (no healthcheck)
//	state=exited|dead|removing|paused   → fatal (service died)
//	state="" (no services yet)          → not ready, keep polling
//
// The function returns ready=true iff at least one service was reported
// AND every reported service is in a counts-toward-ready bucket.
//
// # Why `running/unhealthy` is not fatal
//
// Splice's container healthchecks routinely report `unhealthy` for a
// stretch during onboarding before settling green. The container is
// alive and `restart: always` brings it back if it does crash — so a
// transient `unhealthy` is not proof of irrecoverable failure. The
// WaitForHealthy timeout (with the post-mortem `docker compose ps`
// snapshot in the error message) is the actual gate; classifyHealth's
// job is just "is this snapshot good enough to stop polling?", and the
// honest answer for unhealthy is "no, wait".
//
// `exited`/`dead`/`removing`/`paused` ARE fatal because the container
// is no longer running — no amount of waiting will produce a healthy
// state from there.
func classifyHealth(raw []byte) (ready bool, fatal error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	count := 0
	for scanner.Scan() {
		// Trim only \r (Windows line endings) — NOT all whitespace,
		// because the third column is a tab-delimited empty string for
		// services without a healthcheck and TrimSpace would eat the
		// preceding tab, collapsing 3 columns into 2.
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		// Exactly three columns. If a future `docker compose` release
		// adds more, plain Split would smuggle them into parts[2] and
		// silently break the health-string switch; require the exact
		// shape and keep polling on anything weird.
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return false, nil
		}
		name := parts[0]
		state := parts[1]
		health := strings.TrimSpace(parts[2])
		count++

		switch state {
		case "running":
			switch health {
			case "healthy", "":
				// good
			default:
				// "starting", "unhealthy", or anything unknown — keep
				// polling. Unhealthy is deliberately not fatal; see the
				// decision-table commentary above.
				return false, nil
			}
		case "exited", "dead", "removing", "paused":
			return false, fmt.Errorf("service %q is in state %q", name, state)
		default:
			// "created", "restarting", "" — not ready yet.
			return false, nil
		}
	}
	return count > 0, nil
}

// Endpoints returns container-name → human-readable publisher string,
// e.g. `nginx → "0.0.0.0:54321->2000/tcp, 0.0.0.0:54322->3000/tcp"`. The
// value is intentionally a display blob — the raw {{.Publishers}}
// template output, possibly with multiple comma-separated mappings.
// Callers that need structured port info should use DiscoverPort()
// instead.
//
// Tab is the field separator because Publishers values themselves
// contain spaces; with a tab the right-hand side is always exactly the
// Publishers value, however many ports it holds.
func (c *ComposeRunner) Endpoints(ctx context.Context) map[string]string {
	endpoints := make(map[string]string)
	args := append(c.composeBase(), "ps", "--format", "{{.Name}}\t{{.Publishers}}")
	out, err := c.command(ctx, args...).Output()
	if err != nil {
		return endpoints
	}

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		endpoints[parts[0]] = strings.TrimSpace(parts[1])
	}
	return endpoints
}

// Ps returns a tab-separated docker compose ps snapshot for status rendering.
func (c *ComposeRunner) Ps(ctx context.Context) ([]byte, error) {
	args := append(c.composeBase(), "ps", "--all", "--format", "{{.Name}}\t{{.State}}\t{{.Health}}\t{{.Image}}\t{{.Publishers}}")
	out, err := c.command(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	return out, nil
}

// DiscoverPort returns the host port that the given compose service has
// mapped to its container port. Used when running with TEST_PORT=1 (i.e.
// ephemeral allocation) so we can populate state.Ports with the actual
// daemon-assigned ports.
//
// Returns 0 (not an error) if the service exists but doesn't publish
// the requested container port — caller decides whether that's fatal.
func (c *ComposeRunner) DiscoverPort(ctx context.Context, service string, containerPort int) (int, error) {
	args := append(c.composeBase(), "port", service, fmt.Sprintf("%d", containerPort))
	out, err := c.command(ctx, args...).Output()
	if err != nil {
		// "no port published" returns exit 1 with empty output —
		// surface as port=0 rather than an error.
		return 0, nil
	}
	// Output looks like "0.0.0.0:54321\n". Split on ':' and take the last
	// chunk so IPv6 (e.g. "[::]:54321") also parses.
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	var port int
	if _, err := fmt.Sscanf(s, "%d", &port); err != nil {
		return 0, fmt.Errorf("parse port %q for %s/%d: %w", s, service, containerPort, err)
	}
	return port, nil
}

// Stop runs `docker compose down`. When removeVolumes is true the call
// is destructive — it strips named volumes and orphan containers (the
// `localnet clean` semantics). When false it preserves volumes so a
// follow-up `localnet up` against the same --name can resume from
// existing state (the `localnet down` semantics).
//
// --remove-orphans is always set because forgetting it leaves dangling
// containers after a compose project rename, and the user has no way to
// find or clean them without inspecting docker directly.
//
// TEARDOWN IS `-p`-ONLY — it deliberately omits the `-f` compose files,
// `--env-file`, and `--profile` flags (i.e. it does NOT use
// composeBase). Every Splice LocalNet service is profile-gated
// (`profiles: [sv, app-provider, app-user, multi-sync]`); when `-f` is
// present, `docker compose down` applies profile filtering and removes
// ONLY non-profiled + explicitly-enabled-profile services — which for
// Splice is the empty set. The result is a silent no-op (exit 0) that
// leaves every container running. This is documented compose behavior
// (https://docs.docker.com/compose/how-tos/profiles/#stop-application-and-services-with-specific-profiles),
// not a version bug. Tearing down by project label (`-p`) is
// profile-agnostic and removes the whole project — exactly what `down`
// and `clean` want, and what ForceStop already relies on. See
// CONTRIBUTING.md "Docker Compose teardown must be `-p`-only".
func (c *ComposeRunner) Stop(ctx context.Context, removeVolumes bool) error {
	args := []string{"compose", "-p", c.ProjectName, "down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "--volumes")
	}
	// CombinedOutput so a failure surfaces the real docker error instead
	// of a bare "exit status 1".
	cmd := c.command(ctx, args...)
	// Run from a dir guaranteed to exist so a pruned Splice cache dir
	// (the recorded WorkDir) can't fail the chdir and strand containers.
	cmd.Dir = workDirOrFallback(c.WorkDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if c.LogWriter != nil {
		_, _ = c.LogWriter.Write(out)
	}
	return nil
}

// Down is the destructive teardown, equivalent to Stop(ctx, true).
func (c *ComposeRunner) Down(ctx context.Context) error {
	return c.Stop(ctx, true)
}

// forceCommand builds a docker invocation that inherits the FULL process
// environment (PATH for the compose plugin, etc.) and does not pin Dir or
// Env to the cached project. Force teardown must not depend on the
// project's recorded env/working-dir — which may be exactly what's broken.
func (c *ComposeRunner) forceCommand(ctx context.Context, args ...string) *exec.Cmd {
	if c.commandFn != nil {
		return c.commandFn(ctx, "docker", args...)
	}
	return exec.CommandContext(ctx, "docker", args...)
}

// ForceStop tears the project down by PROJECT LABEL only — it deliberately
// omits the -f compose files and --env-file. A normal, file-driven
// `docker compose down` parses+interpolates those files first and errors
// out (before removing anything) when the env is incomplete or a
// container is unhealthy/OOM-restarting; the label-only form sidesteps
// that, which is the whole point of `down --force`. `--timeout 10`
// SIGKILLs containers that won't stop gracefully. The full docker output
// is captured and returned on failure (no bare "exit status 1").
func (c *ComposeRunner) ForceStop(ctx context.Context, removeVolumes bool) error {
	args := []string{"compose", "-p", c.ProjectName, "down", "--remove-orphans", "--timeout", "10"}
	if removeVolumes {
		args = append(args, "--volumes")
	}
	out, err := c.forceCommand(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down (force) failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if c.LogWriter != nil {
		_, _ = c.LogWriter.Write(out)
	}
	return nil
}

// Pause runs `docker compose pause` — SIGSTOPs every container in the
// project so they hold their in-memory state and stop consuming CPU
// without losing it. Cheap to reverse with Unpause. Unlike Stop it keeps
// the processes alive (no boot cost on resume) and unlike Restart it
// doesn't bounce them. Published host ports stay bound.
func (c *ComposeRunner) Pause(ctx context.Context) error {
	args := append(c.composeBase(), "pause")
	cmd := c.command(ctx, args...)
	cmd.Stdout = c.LogWriter
	cmd.Stderr = c.LogWriter
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose pause failed: %w", err)
	}
	return nil
}

// Unpause runs `docker compose unpause` — SIGCONTs the paused containers,
// resuming them exactly where Pause froze them. No readiness wait is
// needed (the apps never stopped), and ports are unchanged.
func (c *ComposeRunner) Unpause(ctx context.Context) error {
	args := append(c.composeBase(), "unpause")
	cmd := c.command(ctx, args...)
	cmd.Stdout = c.LogWriter
	cmd.Stderr = c.LogWriter
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose unpause failed: %w", err)
	}
	return nil
}

// Restart runs `docker compose restart [services...]`. With no
// services it restarts the whole project. Unlike down+up it keeps
// containers, networks, and volumes — only the processes bounce —
// so it's the right primitive for `localnet restart`. Containers
// keep their identities but Docker MAY re-assign published host
// ports on restart, so callers should re-capture ports afterward.
func (c *ComposeRunner) Restart(ctx context.Context, services ...string) error {
	args := append(c.composeBase(), "restart")
	args = append(args, services...)
	cmd := c.command(ctx, args...)
	cmd.Stdout = c.LogWriter
	cmd.Stderr = c.LogWriter
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose restart failed: %w", err)
	}
	return nil
}
