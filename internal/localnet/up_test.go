package localnet

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestValidateName(t *testing.T) {
	// DNS-label form: lowercase a-z0-9-, no leading/trailing
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

// TestRunUp_RejectsConcurrentSameNameOp covers the concurrent-`up`
// contract: a second `localnet up` against the same name must
// return ExitUserError immediately when another op is holding the
// per-instance lock. Without this, two parallel `up`s would race
// the Docker compose project name collision and produce confusing
// errors.
//
// Test approach: acquire the registry lock directly (as a stand-in
// for "another canton-devkit process is doing something"), then call
// RunUp against the same name. Lock acquisition is step 2 of RunUp,
// before Fetch or compose — so the test doesn't need network or
// Docker, and never reaches the slow code paths.
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

	code := RunUp(ctx, &TextProgress{OutW: &out, ErrW: &errBuf}, &UpOptions{Name: name})

	if code != ExitUserError {
		t.Errorf("expected ExitUserError (%d), got %d\nstdout=%q\nstderr=%q",
			ExitUserError, code, out.String(), errBuf.String())
	}
	stderrText := errBuf.String()
	if !strings.Contains(stderrText, "busy") && !strings.Contains(stderrText, "lock") {
		t.Errorf("expected 'busy' or 'lock' in stderr, got %q", stderrText)
	}
}

// TestRunUp_HappyPath_FakeDriven proves the bring-up orchestration
// sequence end-to-end without docker or network. We swap in two
// seams:
//
//   - FetchFn returns a tempdir pre-populated with env/<role>-auth-on.env
//     files (so captureCredentials reaches signing).
//   - NewRunner returns a stub whose Up + WaitForHealthy both succeed.
//
// We then assert:
//
//   - return code is ExitSuccess
//   - registry.State for --name was written with Status=running
//   - state.Ports contains the five UI keys we hand to compose
//   - state.Credentials has one entry per Splice role
//   - the runner stub saw Up *and* WaitForHealthy called (sequence
//     matters — a regression that called WaitForHealthy before Up
//     would deadlock under real docker)
func TestRunUp_HappyPath_FakeDriven(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	// Build a fake project dir containing the env/ files
	// LoadCredentialInputs expects. Three roles, each with VALIDATOR_USER
	// and AUDIENCE set; everything else can be omitted.
	projectDir := t.TempDir()
	envDir := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	envFiles := map[string]string{
		"sv-auth-on.env":           "AUTH_SV_VALIDATOR_USER_NAME=sv-user\nAUTH_SV_AUDIENCE=sv-aud\n",
		"app-provider-auth-on.env": "AUTH_APP_PROVIDER_VALIDATOR_USER_NAME=ap-user\nAUTH_APP_PROVIDER_AUDIENCE=ap-aud\n",
		"app-user-auth-on.env":     "AUTH_APP_USER_VALIDATOR_USER_NAME=au-user\nAUTH_APP_USER_AUDIENCE=au-aud\n",
	}
	for name, body := range envFiles {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	stub := &composeRunnerStub{}
	opts := &UpOptions{
		Name:          "happy",
		Version:       splice.LatestAlias,
		SkipPreflight: true,
		FetchFn: func(_ context.Context, _ splice.Version, _ string, _ io.Writer) (string, error) {
			return projectDir, nil
		},
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
			return stub
		},
	}

	var out, errBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	code := RunUp(ctx, &TextProgress{OutW: &out, ErrW: &errBuf}, opts)
	if code != ExitSuccess {
		t.Fatalf("RunUp = %d, want ExitSuccess\nstdout=%q\nstderr=%q",
			code, out.String(), errBuf.String())
	}

	// Sequence: Up before WaitForHealthy, both exactly once.
	if stub.upCalls != 1 {
		t.Errorf("compose Up called %d times, want 1", stub.upCalls)
	}
	if stub.waitCalls != 1 {
		t.Errorf("compose WaitForHealthy called %d times, want 1", stub.waitCalls)
	}
	if stub.firstCall != "Up" {
		t.Errorf("first runner call = %q, want %q (Wait before Up would deadlock under real docker)",
			stub.firstCall, "Up")
	}

	// Registry has the expected terminal state.
	state, err := registry.Read("happy")
	if err != nil {
		t.Fatalf("registry.Read: %v", err)
	}
	if state.Status != registry.StatusRunning {
		t.Errorf("state.Status = %q, want %q", state.Status, registry.StatusRunning)
	}
	for _, k := range []string{"app_user_ui", "app_provider_ui", "sv_ui", "swagger_ui", "postgres"} {
		if p, ok := state.Ports[k]; !ok || p <= 0 {
			t.Errorf("state.Ports[%q] = %d (ok=%v); expected non-zero", k, p, ok)
		}
	}
	if len(state.Credentials) != 3 {
		t.Errorf("state.Credentials length = %d, want 3", len(state.Credentials))
	}
}

// TestRunUp_RefusesAlreadyRunning guards the corruption fix: re-running
// `up` against an instance that is already StatusRunning must refuse with
// ExitUserError WITHOUT overwriting the live state. The pre-fix code
// stamped Status=creating, then failed port allocation (the live ports
// are still held) and returned without rollback, stranding the healthy
// instance as a permanent `creating` zombie. Mirrors the Web UI's 409.
func TestRunUp_RefusesAlreadyRunning(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	const name = "already-up"
	seeded := registry.NewState(name, "0.6.4")
	seeded.Status = registry.StatusRunning
	seeded.Ports = map[string]int{"app_user_ui": 12345}
	if err := registry.Write(seeded); err != nil {
		t.Fatalf("seed running instance: %v", err)
	}

	stub := &composeRunnerStub{}
	var out, errBuf bytes.Buffer
	code := RunUp(context.Background(),
		&TextProgress{OutW: &out, ErrW: &errBuf},
		&UpOptions{
			Name:          name,
			Version:       splice.LatestAlias,
			SkipPreflight: true,
			FetchFn: func(_ context.Context, _ splice.Version, _ string, _ io.Writer) (string, error) {
				return t.TempDir(), nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				return stub
			},
		})

	if code != ExitUserError {
		t.Fatalf("RunUp on a running instance = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if stub.upCalls != 0 {
		t.Errorf("compose Up called %d times, want 0 (must refuse before touching docker)", stub.upCalls)
	}
	s, err := registry.Read(name)
	if err != nil {
		t.Fatalf("registry.Read: %v", err)
	}
	if s.Status != registry.StatusRunning {
		t.Errorf("Status = %q, want %q (guard must not overwrite live state)", s.Status, registry.StatusRunning)
	}
	if s.Ports["app_user_ui"] != 12345 {
		t.Errorf("Ports[app_user_ui] = %d, want 12345 (live state was clobbered)", s.Ports["app_user_ui"])
	}
}

// doneRecorder is a Progress that discards everything (via the embedded
// NopProgress) except Done, which it counts. Used to pin that RunUp emits
// the terminal `done` event on success — the signal the Web UI needs to
// leave its "running" modal state.
type doneRecorder struct {
	NopProgress
	doneCalls int
}

func (d *doneRecorder) Done(string) { d.doneCalls++ }

// TestRunUp_EmitsDoneOnSuccess guards the Web UI hang fix: a successful
// bring-up MUST call Progress.Done(). Without it, SSEProgress never
// publishes the terminal marker and the create/resume/restart modal hangs
// on "running" forever, never refreshing the dashboard.
func TestRunUp_EmitsDoneOnSuccess(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	projectDir := t.TempDir()
	envDir := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	for fname, body := range map[string]string{
		"sv-auth-on.env":           "AUTH_SV_VALIDATOR_USER_NAME=sv-user\nAUTH_SV_AUDIENCE=sv-aud\n",
		"app-provider-auth-on.env": "AUTH_APP_PROVIDER_VALIDATOR_USER_NAME=ap-user\nAUTH_APP_PROVIDER_AUDIENCE=ap-aud\n",
		"app-user-auth-on.env":     "AUTH_APP_USER_VALIDATOR_USER_NAME=au-user\nAUTH_APP_USER_AUDIENCE=au-aud\n",
	} {
		if err := os.WriteFile(filepath.Join(envDir, fname), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", fname, err)
		}
	}

	rec := &doneRecorder{}
	code := RunUp(context.Background(), rec, &UpOptions{
		Name:          "done-evt",
		Version:       splice.LatestAlias,
		SkipPreflight: true,
		FetchFn: func(_ context.Context, _ splice.Version, _ string, _ io.Writer) (string, error) {
			return projectDir, nil
		},
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
			return &composeRunnerStub{}
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunUp = %d, want ExitSuccess", code)
	}
	if rec.doneCalls != 1 {
		t.Errorf("Progress.Done called %d times on success, want 1 (Web UI modal hangs without it)", rec.doneCalls)
	}
}

func TestRunUp_AlphaVersionWarns(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	projectDir := t.TempDir()
	envDir := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	for name, body := range map[string]string{
		"sv-auth-on.env":           "AUTH_SV_VALIDATOR_USER_NAME=sv-user\nAUTH_SV_AUDIENCE=sv-aud\n",
		"app-provider-auth-on.env": "AUTH_APP_PROVIDER_VALIDATOR_USER_NAME=ap-user\nAUTH_APP_PROVIDER_AUDIENCE=ap-aud\n",
		"app-user-auth-on.env":     "AUTH_APP_USER_VALIDATOR_USER_NAME=au-user\nAUTH_APP_USER_AUDIENCE=au-aud\n",
	} {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	var out, errBuf bytes.Buffer
	code := RunUp(context.Background(),
		&TextProgress{OutW: &out, ErrW: &errBuf},
		&UpOptions{
			Name:          "alpha-warn",
			Version:       "token-standard-v2",
			SkipPreflight: true,
			FetchFn: func(_ context.Context, _ splice.Version, _ string, _ io.Writer) (string, error) {
				return projectDir, nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				return &composeRunnerStub{}
			},
		})
	if code != ExitSuccess {
		t.Fatalf("RunUp = %d, want ExitSuccess\nstdout=%q\nstderr=%q", code, out.String(), errBuf.String())
	}
	if !strings.Contains(errBuf.String(), `selecting alpha-channel Splice "token-standard-v2"`) {
		t.Errorf("stderr missing alpha warning\nfull:\n%s", errBuf.String())
	}
}

// TestRunUp_UncuratedTagWithoutOptInRejected locks in the security
// floor of the two-layer version model: without
// --allow-uncurated, a tag that isn't in the curated catalogue must
// be rejected as a user error BEFORE any expensive work.
func TestRunUp_UncuratedTagWithoutOptInRejected(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // isolate resolved-versions.json

	var out, errBuf bytes.Buffer
	code := RunUp(context.Background(), &TextProgress{OutW: &out, ErrW: &errBuf}, &UpOptions{
		Name:           "uncurated-default",
		Version:        "0.99.0-this-tag-does-not-exist",
		SkipPreflight:  true,
		AllowUncurated: false,
	})
	if code != ExitUserError {
		t.Fatalf("RunUp code = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "--allow-uncurated") {
		t.Errorf("stderr should hint at --allow-uncurated, got %q", errBuf.String())
	}
}

// composeRunnerStub is the fake injected via UpOptions.NewRunner in
// the happy-path test. It records call order so a regression that
// flips Up/WaitForHealthy is caught immediately.
type composeRunnerStub struct {
	upCalls, waitCalls int
	firstCall          string
}

func (s *composeRunnerStub) Up(context.Context) error {
	s.upCalls++
	if s.firstCall == "" {
		s.firstCall = "Up"
	}
	return nil
}

func (s *composeRunnerStub) WaitForHealthy(context.Context) error {
	s.waitCalls++
	if s.firstCall == "" {
		s.firstCall = "WaitForHealthy"
	}
	return nil
}

// TestRunUp_CLIByteEquivalence pins the CLI output contract: the
// TextProgress-backed RunUp must emit the same set of header lines
// today's users see, AND must NOT emit new lines for the five
// "silent" steps (resolve, lock, fetch, persist, capture_jwts) —
// adding terminal noise for those is a deliberate CLI behaviour
// change, not a side-effect of refactoring.
//
// Asserts on substring presence rather than full byte sequence so a
// future tweak to the "Starting Canton LocalNet ..." header
// formatting doesn't fail spuriously — the spirit of the contract
// is "these phases are visible / those phases are silent."
func TestRunUp_CLIByteEquivalence(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	projectDir := t.TempDir()
	envDir := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	// Minimal env files so captureCredentials doesn't error mid-run.
	for name, body := range map[string]string{
		"sv-auth-on.env":           "AUTH_SV_VALIDATOR_USER_NAME=u\nAUTH_SV_AUDIENCE=a\n",
		"app-provider-auth-on.env": "AUTH_APP_PROVIDER_VALIDATOR_USER_NAME=u\nAUTH_APP_PROVIDER_AUDIENCE=a\n",
		"app-user-auth-on.env":     "AUTH_APP_USER_VALIDATOR_USER_NAME=u\nAUTH_APP_USER_AUDIENCE=a\n",
	} {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	var out, errBuf bytes.Buffer
	code := RunUp(context.Background(),
		&TextProgress{OutW: &out, ErrW: &errBuf},
		&UpOptions{
			Name:          "cli-bytes",
			Version:       splice.LatestAlias,
			SkipPreflight: true,
			FetchFn: func(_ context.Context, _ splice.Version, _ string, _ io.Writer) (string, error) {
				return projectDir, nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				return &composeRunnerStub{}
			},
		})
	if code != ExitSuccess {
		t.Fatalf("RunUp = %d; stdout=%q stderr=%q", code, out.String(), errBuf.String())
	}

	stdout := out.String()

	// VISIBLE step lines — must be present.
	mustContain := []string{
		"Starting Canton LocalNet",                  // the verbatim header preserved via prog.Out()
		"Starting services...",                      // StepStartServices via TextProgress.StartStep
		"Waiting for services to become healthy...", // StepWaitHealthy
		"is ready", // Done() success marker (welcome line: `"x" is ready · Splice …`)
	}
	// SkipPreflight is true in this test, so "Running preflight checks..."
	// is intentionally absent. The non-test CLI run hits that path and
	// TextProgress_StartStepOutput pins its byte form.
	for _, want := range mustContain {
		if !strings.Contains(stdout, want) {
			t.Errorf("CLI stdout missing %q\nfull:\n%s", want, stdout)
		}
	}

	// SILENT step labels — must NOT appear in CLI output. Adding any
	// of these would be a CLI behaviour change, not a refactor side-
	// effect; this guard catches accidental promotion of a step into
	// textVisibleSteps.
	mustNotContain := []string{
		stepLabel[StepResolveVersion],
		stepLabel[StepAcquireLock],
		stepLabel[StepFetchSplice],
		stepLabel[StepPersistState],
		stepLabel[StepCaptureJWTs],
	}
	for _, banned := range mustNotContain {
		if strings.Contains(stdout, banned) {
			t.Errorf("silent step leaked into CLI stdout: %q\nfull:\n%s",
				banned, stdout)
		}
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
