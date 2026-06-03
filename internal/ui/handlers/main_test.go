package handlers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// TestMain installs a process-wide CANTON_DEVKIT_REGISTRY pointing at
// a fresh tempdir BEFORE any test starts. Reason: handleCreate (and a
// few siblings) spawn goroutines that outlive the test body. If a
// goroutine reads registry.Root() after t.Setenv has reverted, it
// falls back to the real user home and explodes in CI where
// `~/.canton-devkit` is unwritable (BIT-184 CI break).
//
// Per-test t.Setenv overrides still work — they revert to *this*
// tempdir after each test rather than to the real home, so leaked
// goroutines write into a sandbox instead of failing the run.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "devkit-handlers-tests-*")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "TestMain: mkdir tempdir:", err)
		os.Exit(2)
	}
	if err := os.Setenv("CANTON_DEVKIT_REGISTRY", root); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "TestMain: setenv:", err)
		os.Exit(2)
	}

	// Stub the preflight seam so handler tests don't depend on the
	// CI runner's docker daemon meeting the version-specific memory
	// floor. Production code path is the real implementation; the
	// real path is exercised by integration tests, not unit tests.
	// (BIT-184 CI break: the self-hosted runner returned
	// DOCKER_MEMORY_LOW from RunPreflight, causing handleCreate to
	// short-circuit with 422 before the test could reach its
	// 202-assertion.)
	runPreflightForVersion = func(_ context.Context, _ splice.Version) types.PreflightReport {
		return types.PreflightReport{SchemaVersion: types.SchemaVersion, OK: true}
	}

	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
