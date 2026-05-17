package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	readinessTimeout  = 120 * time.Second
	readinessPollWait = 2 * time.Second
)

type ComposeRunner struct {
	ProjectName string
	ComposeFile string
	WorkDir     string
	LogWriter   io.Writer
}

func (c *ComposeRunner) Up(ctx context.Context) error {
	args := []string{
		"compose",
		"-p", c.ProjectName,
		"-f", c.ComposeFile,
		"up", "-d", "--wait",
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = c.WorkDir
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
	args := []string{
		"compose",
		"-p", c.ProjectName,
		"-f", c.ComposeFile,
		"ps", "--format", "{{.Health}}",
	}

	out, err := exec.CommandContext(ctx, "docker", args...).Output()
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
	args := []string{
		"compose",
		"-p", c.ProjectName,
		"-f", c.ComposeFile,
		"ps", "--format", "{{.Name}} {{.Publishers}}",
	}

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

func (c *ComposeRunner) Down(ctx context.Context) error {
	args := []string{
		"compose",
		"-p", c.ProjectName,
		"-f", c.ComposeFile,
		"down", "--volumes", "--remove-orphans",
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = c.WorkDir
	return cmd.Run()
}

func ComposeFilePath(dataDir string) string {
	return filepath.Join(dataDir, "docker-compose.yml")
}
