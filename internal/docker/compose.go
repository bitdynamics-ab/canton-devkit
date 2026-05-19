package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	// Splice LocalNet's onboarding flow (DAML package vetting, SV
	// keygen, validator registration) routinely takes 3–5 minutes on a
	// cold start. 15 minutes gives slow hosts plenty of headroom; the
	// caller can still ^C earlier.
	readinessTimeout  = 15 * time.Minute
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
	// every `docker compose` invocation.
	Env       []string
	WorkDir   string
	LogWriter io.Writer
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
	return args
}

func (c *ComposeRunner) Up(ctx context.Context) error {
	// We omit --wait deliberately: compose's --wait surfaces transient
	// "unhealthy" states as fatal during long Splice onboarding.
	// WaitForHealthy below does its own polling with a Splice-sized
	// timeout.
	args := append(c.composeBase(), "up", "-d")

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = c.WorkDir
	cmd.Stdout = c.LogWriter
	cmd.Stderr = c.LogWriter
	if c.Env != nil {
		cmd.Env = c.Env
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	return nil
}

func (c *ComposeRunner) WaitForHealthy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for services to become healthy")
		default:
		}

		if c.allHealthy(ctx) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for services to become healthy")
		case <-time.After(readinessPollWait):
		}
	}
}

func (c *ComposeRunner) allHealthy(ctx context.Context) bool {
	args := append(c.composeBase(), "ps", "--format", "{{.Health}}")

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = c.WorkDir
	if c.Env != nil {
		cmd.Env = c.Env
	}
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line != "healthy" {
			return false
		}
		count++
	}
	return count > 0
}

func (c *ComposeRunner) Endpoints() map[string]string {
	endpoints := make(map[string]string)
	args := append(c.composeBase(), "ps", "--format", "{{.Name}} {{.Publishers}}")

	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return endpoints
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				endpoints[parts[0]] = parts[1]
			}
		}
	}
	return endpoints
}

// DiscoverPort returns the host port that the given compose service has
// mapped to its container port. Used when running with TEST_PORT=1 (i.e.
// PortEphemeral) so we can populate state.Ports with the actual
// daemon-assigned ports.
//
// Returns 0 (not an error) if the service exists but doesn't publish
// the requested container port — caller decides whether that's fatal.
func (c *ComposeRunner) DiscoverPort(ctx context.Context, service string, containerPort int) (int, error) {
	args := append(c.composeBase(), "port", service, fmt.Sprintf("%d", containerPort))
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = c.WorkDir
	if c.Env != nil {
		cmd.Env = c.Env
	}
	out, err := cmd.Output()
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

func (c *ComposeRunner) Down(ctx context.Context) error {
	args := append(c.composeBase(), "down", "--volumes", "--remove-orphans")

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = c.WorkDir
	return cmd.Run()
}
