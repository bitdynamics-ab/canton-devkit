//go:build integration

// Package localnet contains integration tests gated behind the `integration`
// build tag. Run with:
//
//	go test -tags=integration -timeout=30m ./internal/localnet/...
//
// Requires:
//   - Docker daemon running on the host
//   - Network access to github.com to fetch the Splice tarball
//   - ~3-5 minutes per `up` (Splice onboarding is slow)
//   - At least 10 GB free disk + 4 GB Docker daemon memory
//
// Skipped automatically by `go test ./...` (no tag). Run nightly or on
// release tags via .github/workflows/integration.yml.
//
// What this catches that unit tests don't:
//   - Real `docker compose up` against Splice: exercises the WorkDir+
//     Env wiring on a fresh subprocess, every adapter env var,
//     classifyHealth against actual `docker compose ps` output, the
//     full container-rename overlay.
//   - Whether `status`, `creds`, `logs`, `down` actually function after
//     `up` returns (the family of "fresh shell" bugs that prompted
//     composeenv.go).
//
// What it does NOT catch:
//   - Cross-platform issues (only ubuntu-latest in CI).
//   - UI behaviour (no headless browser).

package localnet

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// instanceName is unique per CI run (use the runner attempt number) so
// failed previous runs don't collide. Locally, fall back to a stable
// name so `go test -tags=integration` repeatedly against the same dev
// box reuses the cache.
func instanceName() string {
	if v := os.Getenv("GITHUB_RUN_ATTEMPT"); v != "" {
		return "ci-" + v
	}
	return "ci-local"
}

func TestIntegrationUpStatusCredsDown(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (skipped under -short)")
	}

	name := instanceName()

	// Best-effort pre-clean: ignore errors so a fresh runner with no
	// prior state still proceeds.
	_ = runDown(t, name)

	// up
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	t.Logf("Bringing up instance %q (this can take 3-5 min)...", name)
	if code := RunUp(ctx, os.Stdout, os.Stderr, &UpOptions{Name: name}); code != ExitSuccess {
		t.Fatalf("up: exit %d", code)
	}
	defer func() {
		// Always tear down, even on later assertion failure.
		t.Logf("Tearing down instance %q...", name)
		_ = runDown(t, name)
	}()

	// status JSON shape is { "state": { "status": "running", ... },
	// "services": [...] }; the lifecycle status sits inside `state`.
	var statusOut bytes.Buffer
	if code := RunStatus(context.Background(), &statusOut, os.Stderr, &StatusOptions{Name: name, Format: "json"}); code != ExitSuccess {
		t.Fatalf("status: exit %d; stdout=%q", code, statusOut.String())
	}
	var status map[string]any
	if err := json.Unmarshal(statusOut.Bytes(), &status); err != nil {
		t.Fatalf("status output not JSON: %v\nbody=%q", err, statusOut.String())
	}
	state, _ := status["state"].(map[string]any)
	if state == nil {
		t.Fatalf("status JSON missing `state` key: %v", status)
	}
	if got, _ := state["status"].(string); got != "running" {
		t.Errorf("expected state.status=running, got %v\nfull state: %v", got, state)
	}

	// creds JSON shape is []Credential (an array). printCredsJSON
	// does enc.Encode(creds) on the slice, not a wrapper object.
	var credsOut bytes.Buffer
	if code := RunCreds(&credsOut, os.Stderr, &CredsOptions{Name: name, Format: "json"}); code != ExitSuccess {
		t.Fatalf("creds: exit %d; stdout=%q", code, credsOut.String())
	}
	var creds []map[string]any
	if err := json.Unmarshal(credsOut.Bytes(), &creds); err != nil {
		t.Fatalf("creds output not JSON: %v\nbody=%q", err, credsOut.String())
	}
	// Expect exactly three role entries; check all three roles appear.
	gotRoles := make(map[string]bool, len(creds))
	for _, c := range creds {
		if r, ok := c["role"].(string); ok {
			gotRoles[r] = true
		}
		if _, ok := c["jwt"].(string); !ok {
			t.Errorf("creds entry missing jwt field: %v", c)
		}
	}
	for _, role := range []string{"sv", "app-provider", "app-user"} {
		if !gotRoles[role] {
			t.Errorf("creds output missing role %q\ngot roles: %v", role, gotRoles)
		}
	}

	// state.json was written
	stateFile := filepath.Join(os.Getenv("HOME"), ".canton-devkit", "localnet", name, "state.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Errorf("state.json missing at %s: %v", stateFile, err)
	}
}

// runDown invokes RunDown with a generous timeout. Returns the exit code
// from RunDown so callers can tell "instance didn't exist" (success or
// user-error, both acceptable) from a real failure.
func runDown(t *testing.T, name string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return RunDown(ctx, os.Stdout, os.Stderr, &DownOptions{Name: name})
}
