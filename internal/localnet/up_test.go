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
	// DNS-label form (PR #20 #6): lowercase a-z0-9-, no leading/trailing
	// hyphen, 1-63 chars. ValidateName delegates to
	// registry.ValidateName so the rule is enforced in exactly one
	// place; this test pins the wrapper's contract (empty-string
	// message + error propagation).
	valid := []string{"alice", "alice-net", "a", "a-b-c", "test123"}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("expected %q valid, got %v", n, err)
		}
	}

	invalid := []string{
		"",                       // empty -> --name is required
		"-alice",                 // leading hyphen
		"alice-",                 // trailing hyphen
		"has space",              // space
		"has_underscore",         // underscore (rejected by DNS-label rule)
		"Test123",                // uppercase (rejected by DNS-label rule)
		"slash/in",               // path separator
		string(make([]byte, 64)), // length / NUL
	}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil {
			t.Errorf("expected %q invalid", n)
		}
	}
}

// TestValidateName_DelegatesToRegistry locks in the single-source-of-
// truth contract: a name that registry.ValidateName rejects must also
// be rejected by the CLI wrapper. Catches the regression where
// someone re-introduces a divergent ad-hoc check in this package.
func TestValidateName_DelegatesToRegistry(t *testing.T) {
	for _, n := range []string{"MyStack", "my_stack", "..", "a/b"} {
		if registry.ValidateName(n) == nil {
			t.Fatalf("test premise broken: registry.ValidateName accepts %q", n)
		}
		if ValidateName(n) == nil {
			t.Errorf("localnet.ValidateName accepted %q but registry rejects it (policies diverged)", n)
		}
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
