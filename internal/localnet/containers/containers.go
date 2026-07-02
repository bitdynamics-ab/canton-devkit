// Package containers exposes the per-container operations shared
// between the CLI (`dpm localnet container …` subcommands) and the
// Web UI's HTTP handlers (POST /api/instances/{name}/containers/…).
//
// Two surfaces, one set of docker-side functions, per the
// CONTRIBUTING.md "CLI ↔ Web UI parity" rule: validation lives at
// the edge (HTTP handler for query-params, cobra command for
// flags), the docker work itself lives here exactly once.
//
// Functions in this package never read the registry directly —
// callers pass the resolved compose project + container name in,
// keeping this package free of registry locking concerns.
package containers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Info is the normalized per-container snapshot returned by List.
// Fields mirror what `docker compose ps --format json` emits;
// unknown columns (ExitCode, RunningFor, …) are deliberately
// dropped so a docker version bump that adds fields doesn't break
// the JSON decoder. The HTTP handler wraps each Info into its API
// type; CLI consumers use Info directly.
type Info struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	State   string `json:"state"`            // running / restarting / exited / dead / paused / created
	Health  string `json:"health,omitempty"` // healthy / unhealthy / starting / "" (no healthcheck)
	Status  string `json:"status"`           // raw human string ("Up 4 minutes (health: starting)")
	Image   string `json:"image,omitempty"`
}

// List runs `docker compose -p <project> ps --all --format json`
// and parses the result. Returns an empty slice (no error) when
// docker reports no containers — the project may have been torn
// down out-of-band, or never created.
//
// Error path is reserved for docker-side failures (daemon down,
// compose binary missing) — handler turns those into 503, CLI
// surfaces them as ExitRuntimeFailure.
func List(ctx context.Context, project string) ([]Info, error) {
	cmd := exec.CommandContext(ctx,
		"docker", "compose", "-p", project,
		"ps", "--all", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(out) == 0 {
			stderrText := string(exitErr.Stderr)
			if strings.Contains(stderrText, "no such") ||
				strings.Contains(stderrText, "not exist") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}

	// docker compose ps --format json emits a single JSON ARRAY
	// in newer versions (>= v2.21) or NDJSON in older. Handle both.
	trimmed := bytes.TrimLeft(out, " \t\r\n")
	infos := []Info{}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []dockerComposePsEntry
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, fmt.Errorf("parse compose ps JSON array: %w", err)
		}
		for _, e := range arr {
			infos = append(infos, Info(e))
		}
		return infos, nil
	}
	// NDJSON fallback.
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e dockerComposePsEntry
		if err := json.Unmarshal(line, &e); err != nil {
			// Best-effort: skip a malformed line rather than
			// failing the whole probe.
			continue
		}
		infos = append(infos, Info(e))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan compose ps NDJSON: %w", err)
	}
	return infos, nil
}

// BelongsToProject verifies container is one of the containers in
// project. Returns ErrContainerNotInProject if not. Defence-in-
// depth used by both the HTTP layer (so a malicious request can't
// log/restart an arbitrary host container by passing a foreign
// name) and the CLI (same risk if a user typos a container name
// — better to fail loudly than to operate on a stranger's
// container).
func BelongsToProject(ctx context.Context, project, container string) error {
	infos, err := List(ctx, project)
	if err != nil {
		return err
	}
	for _, c := range infos {
		if c.Name == container {
			return nil
		}
	}
	return fmt.Errorf("%w: container %q not in compose project %q",
		ErrContainerNotInProject, container, project)
}

// ErrContainerNotInProject is returned by BelongsToProject and
// surfaces upward to 404 (HTTP) or ExitUserError (CLI).
var ErrContainerNotInProject = errors.New("container not in project")

// LogsOptions controls Logs. Zero values fall back to defaults:
// Tail=200, no Since filter.
type LogsOptions struct {
	Tail  int    // 0 → 200; clamped to [10, 2000]
	Since string // "" disables; otherwise must match `<number><h|m|s>`
}

// Logs runs `docker logs --tail N [--since DUR] <container>` and
// returns the combined stdout+stderr. Caller is responsible for
// having already validated `container` belongs to its project
// (call BelongsToProject first if the source of `container` is
// user-supplied).
//
// Docker logs are written to stderr for the bootstrap and stdout
// for application output; we merge so the user sees one stream.
//
// On partial failure (docker exits non-zero but produced SOME
// output), the partial output is returned along with the error so
// the caller can render what's available.
func Logs(ctx context.Context, container string, opts LogsOptions) (string, error) {
	tail := opts.Tail
	if tail == 0 {
		tail = 200
	}
	if tail < 10 {
		tail = 10
	}
	if tail > 2000 {
		tail = 2000
	}

	args := []string{"logs", "--tail", strconv.Itoa(tail)}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	args = append(args, container)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	runErr := cmd.Run()
	out := combined.String()
	if runErr != nil && out == "" {
		return "", fmt.Errorf("docker logs %s: %w", container, runErr)
	}
	if runErr != nil {
		// Return what we got + the error so callers can decide.
		return out, fmt.Errorf("docker logs %s (partial output): %w", container, runErr)
	}
	return out, nil
}

// Restart runs `docker restart <container>` and waits for it to
// complete. The default 30s timeout matches docker's stop grace
// (10s) plus typical startup; callers needing longer must pass a
// ctx with their own deadline.
func Restart(ctx context.Context, container string) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "docker", "restart", container)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		cause := strings.TrimSpace(stderr.String())
		if cause == "" {
			cause = err.Error()
		}
		return fmt.Errorf("docker restart %s: %s", container, firstLine(cause))
	}
	return nil
}

// firstLine returns just the first newline-terminated line of s.
// Used for compact error messages so a multi-line docker stderr
// dump doesn't blow up an HTTP error envelope or a CLI status line.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// dockerComposePsEntry mirrors the JSON fields `docker compose ps
// --format json` emits. Unexported because callers should consume
// Info instead.
type dockerComposePsEntry struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
	Status  string `json:"Status"`
	Image   string `json:"Image"`
}
