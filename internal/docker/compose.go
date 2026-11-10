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
	readinessTimeout  = 25 * time.Minute
	readinessPollWait = 3 * time.Second
)

type ComposeRunner struct {
	ProjectName  string
	ComposeFiles []string
	EnvFiles     []string
	Env          []string
	WorkDir      string
	LogWriter    io.Writer
	Profiles     []string

	commandFn func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

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

func workDirOrFallback(dir string) string {
	if dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return os.TempDir()
}

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

func (c *ComposeRunner) composeBase() []string {
	args := []string{"compose", "-p", c.ProjectName}
	for _, f := range c.ComposeFiles {
		args = append(args, "-f", f)
	}
	for _, ef := range c.EnvFiles {
		args = append(args, "--env-file", ef)
	}
	for _, p := range c.Profiles {
		args = append(args, "--profile", p)
	}
	return args
}

func (c *ComposeRunner) Up(ctx context.Context) error {
	// --wait is omitted deliberately: compose's --wait surfaces transient
	// "unhealthy" states as fatal during long Splice onboarding, so
	// WaitForHealthy does its own polling with a Splice-sized timeout.
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
		if state == "running" && (health == "healthy" || health == "") {
			continue
		}
		if state != "running" || health != "starting" || !strings.HasSuffix(name, "-splice") {
			return false
		}
		sawSplice = true
	}
	return sawSplice
}

func (c *ComposeRunner) spliceReadyzOK(ctx context.Context, raw []byte) bool {
	name := spliceContainerName(raw)
	if name == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := c.command(probeCtx,
		"exec", name,
		"curl", "-sf", "-o", "/dev/null",
		"http://localhost:2903/api/validator/readyz")
	return cmd.Run() == nil
}

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

func (c *ComposeRunner) healthSnapshot(ctx context.Context) (ready bool, fatal error) {
	_, ready, fatal = c.healthSnapshotRaw(ctx)
	return ready, fatal
}

func (c *ComposeRunner) healthSnapshotRaw(ctx context.Context) (raw []byte, ready bool, fatal error) {
	args := append(c.composeBase(), "ps", "--all", "--format", "{{.Name}}\t{{.State}}\t{{.Health}}")
	out, err := c.command(ctx, args...).Output()
	if err != nil {
		return nil, false, nil
	}
	ready, fatal = classifyHealth(out)
	return out, ready, fatal
}

// Splice's container healthchecks routinely report `unhealthy` for a
// stretch during onboarding before settling green. The container is
// alive and `restart: always` brings it back if it does crash — so a
// transient `unhealthy` is not proof of irrecoverable failure. The
// WaitForHealthy timeout (with the post-mortem `docker compose ps`
// snapshot in the error message) is the actual gate; classifyHealth's
// job is just "is this snapshot good enough to stop polling?", and the
// honest answer for unhealthy is "no, wait".
func classifyHealth(raw []byte) (ready bool, fatal error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	count := 0
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
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
			default:
				return false, nil
			}
		case "exited", "dead", "removing", "paused":
			return false, fmt.Errorf("service %q is in state %q", name, state)
		default:
			return false, nil
		}
	}
	return count > 0, nil
}

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

func (c *ComposeRunner) Ps(ctx context.Context) ([]byte, error) {
	args := append(c.composeBase(), "ps", "--all", "--format", "{{.Name}}\t{{.State}}\t{{.Health}}\t{{.Image}}\t{{.Publishers}}")
	out, err := c.command(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	return out, nil
}

func (c *ComposeRunner) DiscoverPort(ctx context.Context, service string, containerPort int) (int, error) {
	args := append(c.composeBase(), "port", service, fmt.Sprintf("%d", containerPort))
	out, err := c.command(ctx, args...).Output()
	if err != nil {
		// "no port published" returns exit 1 with empty output —
		// surface as port=0 rather than an error.
		return 0, nil
	}
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
// and `remove` want, and what ForceStop already relies on. See
// CONTRIBUTING.md "Docker Compose teardown must be `-p`-only".
func (c *ComposeRunner) Stop(ctx context.Context, removeVolumes bool) error {
	args := []string{"compose", "-p", c.ProjectName, "down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "--volumes")
	}
	cmd := c.command(ctx, args...)
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

func (c *ComposeRunner) Down(ctx context.Context) error {
	return c.Stop(ctx, true)
}

func (c *ComposeRunner) forceCommand(ctx context.Context, args ...string) *exec.Cmd {
	if c.commandFn != nil {
		return c.commandFn(ctx, "docker", args...)
	}
	return exec.CommandContext(ctx, "docker", args...)
}

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

// TEARDOWN IS `-p`-ONLY for the same reason Stop is (see Stop's godoc):
// every Splice service is profile-gated, so a `-f`-driven `compose stop`
// would apply profile filtering and no-op. Targeting by project label is
// profile-agnostic and stops the whole project. See CONTRIBUTING.md
// "Docker Compose teardown must be `-p`-only".
func (c *ComposeRunner) StopContainers(ctx context.Context) error {
	cmd := c.command(ctx, "compose", "-p", c.ProjectName, "stop")
	cmd.Dir = workDirOrFallback(c.WorkDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose stop failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if c.LogWriter != nil {
		_, _ = c.LogWriter.Write(out)
	}
	return nil
}

// `-p`-only for the same profile-filtering reason as StopContainers.
func (c *ComposeRunner) Start(ctx context.Context) error {
	cmd := c.command(ctx, "compose", "-p", c.ProjectName, "start")
	cmd.Dir = workDirOrFallback(c.WorkDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose start failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if c.LogWriter != nil {
		_, _ = c.LogWriter.Write(out)
	}
	return nil
}
