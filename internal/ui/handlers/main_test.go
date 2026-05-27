package handlers

import (
	"fmt"
	"os"
	"testing"
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
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
