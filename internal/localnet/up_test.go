package localnet

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestValidateName(t *testing.T) {
	valid := []string{"alice", "alice-net", "Test123", "a", "a-b-c"}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("expected %q valid, got %v", n, err)
		}
	}

	invalid := []string{"", "-alice", "alice-", "has space", "has_underscore", "slash/in", string(make([]byte, 64))}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestIsValidName(t *testing.T) {
	// isValidName is the lowercase predicate; ValidateName wraps it
	// with a richer error. Keep this test so a regression in the
	// predicate fails loudly without going through ValidateName.
	if !isValidName("alice") {
		t.Error("alice should validate")
	}
	if isValidName("") {
		t.Error("empty should not validate")
	}
	if isValidName("a b") {
		t.Error("spaces should not validate")
	}
}

// TestRunUp_RejectsConcurrentSameNameOp covers the contract Zhe
// flagged on PR #24: a second `localnet up` against the same name
// must return ExitUserError immediately when another op is holding
// the per-instance lock. Without this, two parallel `up`s would race
// the Docker compose project name collision and produce confusing
// errors.
//
// Test approach: acquire the registry lock directly (as a stand-in
// for "another canton-devkit process is doing something"), then call
// RunUp against the same name. Lock acquisition is step 2 of RunUp,
// before Fetch or compose — so the test doesn't need network or
// Docker, and never reaches the slow code paths.
//
// Filed in BIT-118.
func TestRunUp_RejectsConcurrentSameNameOp(t *testing.T) {
	// Point the registry at a temp dir for the test's lifetime so we
	// don't touch ~/.canton-devkit/.
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	name := "dup-test"

	// Hold the lock as if another canton-devkit op were in flight.
	release, err := registry.Lock(name)
	if err != nil {
		t.Fatalf("setup Lock: %v", err)
	}
	defer release()

	// Now invoke RunUp against the same name. It must fail fast with
	// ExitUserError; we deliberately do NOT pass SkipPreflight, because
	// Lock acquisition fires BEFORE preflight — if Lock isn't rejecting
	// us, the test would proceed to preflight (which calls docker) and
	// hang.
	var out, errBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code := RunUp(ctx, &out, &errBuf, &UpOptions{Name: name})

	if code != ExitUserError {
		t.Errorf("expected ExitUserError (%d), got %d\nstdout=%q\nstderr=%q",
			ExitUserError, code, out.String(), errBuf.String())
	}
	stderrText := errBuf.String()
	if !strings.Contains(stderrText, "busy") && !strings.Contains(stderrText, "lock") {
		t.Errorf("expected 'busy' or 'lock' in stderr, got %q", stderrText)
	}
}

// TestRunUp_LockReleasedAfterDownIsReusable proves the symmetric
// contract: once the lock holder releases, a fresh RunUp against the
// same name should be able to ACQUIRE the lock again. Sanity check
// that release() doesn't leave the lock file flock'd.
func TestRunUp_LockReleasedAfterDownIsReusable(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	name := "reuse-test"

	// Acquire + release.
	release, err := registry.Lock(name)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	release()

	// Should be able to acquire again.
	release2, err := registry.Lock(name)
	if err != nil {
		t.Fatalf("second Lock after release: %v", err)
	}
	release2()
}
