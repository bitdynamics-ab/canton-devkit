package containers

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

// FollowLine is one log line emitted by Follow.
type FollowLine struct {
	Text   string
	Stream string // "stdout" or "stderr" — we merge so this is informational only
}

// Follow streams a container's log lines into the returned channel
// until ctx is cancelled or the container exits. Runs
// `docker logs --follow [--since DUR] [--tail N] <container>` and
// scans the merged stdout+stderr line-by-line. Used by the CLI's
// logs TUI; the HTTP layer uses Logs (one-shot tail).
//
// Channel semantics:
//   - Lines arrive in order.
//   - The line channel closes when ctx is done OR docker exits.
//   - The error channel sees at most one error (the cause of close),
//     then closes.
//
// Caller MUST drain both channels — leaking either will block the
// reader goroutines.
func Follow(ctx context.Context, container string, opts LogsOptions) (<-chan FollowLine, <-chan error) {
	lines := make(chan FollowLine, 256)
	errs := make(chan error, 1)

	args := []string{"logs", "--follow"}
	if opts.Tail > 0 {
		// Same clamps as Logs() for consistency.
		t := opts.Tail
		if t < 10 {
			t = 10
		}
		if t > 2000 {
			t = 2000
		}
		args = append(args, "--tail", strconv.Itoa(t))
	}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	args = append(args, container)

	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		errs <- fmt.Errorf("docker logs follow stdout pipe: %w", err)
		close(lines)
		close(errs)
		return lines, errs
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		errs <- fmt.Errorf("docker logs follow stderr pipe: %w", err)
		close(lines)
		close(errs)
		return lines, errs
	}
	if err := cmd.Start(); err != nil {
		errs <- fmt.Errorf("docker logs follow start: %w", err)
		close(lines)
		close(errs)
		return lines, errs
	}

	// Two scanner goroutines — one per stream. They both write into
	// `lines`. A separate goroutine waits on cmd.Wait() and closes
	// both channels when both scanners have drained.
	done := make(chan struct{}, 2)
	scan := func(stream string, s *bufio.Scanner) {
		// Allow long lines (Canton's JSON log lines can be > 64 KiB).
		s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for s.Scan() {
			select {
			case <-ctx.Done():
				done <- struct{}{}
				return
			case lines <- FollowLine{Text: s.Text(), Stream: stream}:
			}
		}
		done <- struct{}{}
	}
	go scan("stdout", bufio.NewScanner(stdout))
	go scan("stderr", bufio.NewScanner(stderr))

	go func() {
		<-done
		<-done
		// Both scanners drained; reap the subprocess.
		waitErr := cmd.Wait()
		close(lines)
		if waitErr != nil && ctx.Err() == nil {
			errs <- fmt.Errorf("docker logs %s exited: %w", container, waitErr)
		}
		close(errs)
	}()

	return lines, errs
}
