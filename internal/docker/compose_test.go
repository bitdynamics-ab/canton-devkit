package docker

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// recorder captures every *exec.Cmd that ComposeRunner.command() builds.
// We use this in lieu of writing a fake docker binary to a temp dir
// because:
//
//   - The wiring assertions reviewers care about (project name, compose
//     files, env files, WorkDir, Env, argv order) are all observable on
//     the *exec.Cmd struct — no need to actually run docker.
//   - The behaviour assertions (WaitForHealthy under exited/unhealthy/
//     starting/healthy/no-services) are exercised against classifyHealth
//     directly, which keeps test runtime <50ms and removes any reliance
//     on a shell being present.
//
// For methods that consume the command's stdout (healthSnapshot,
// Endpoints, DiscoverPort) we still need a real *exec.Cmd that produces
// scripted output — see scriptedCmd, which uses /bin/sh -c. Those tests
// are skipped on platforms without /bin/sh; the wiring tests cover the
// same surface portably.
type recorder struct {
	calls []*exec.Cmd
}

func (r *recorder) factory(ctx context.Context, name string, arg ...string) *exec.Cmd {
	// Use `true` (POSIX) — it succeeds with empty output. Wiring tests
	// don't invoke .Run/.Output, so even on Windows where `true` isn't
	// guaranteed this never executes.
	cmd := exec.CommandContext(ctx, "true")
	cmd.Args = append([]string{name}, arg...)
	r.calls = append(r.calls, cmd)
	return cmd
}

// scriptedRecorder is the variant used by behaviour tests: it returns a
// real *exec.Cmd that, when .Output() runs, emits the scripted stdout
// and exits with the scripted code.
type scriptedRecorder struct {
	calls []*exec.Cmd
	argvs [][]string // logical docker argv, captured separately because
	// cmd.Args belongs to /bin/sh for real execution.
	script func(args []string) (stdout string, exitCode int)
}

func (r *scriptedRecorder) factory(ctx context.Context, name string, arg ...string) *exec.Cmd {
	argv := append([]string{name}, arg...)
	stdout, exit := r.script(argv)
	shellScript := "printf %s " + shellQuote(stdout) + "; exit " + strconv.Itoa(exit)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", shellScript)
	r.calls = append(r.calls, cmd)
	r.argvs = append(r.argvs, argv)
	return cmd
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runnerForWiring builds a ComposeRunner with realistic config and a
// recorder factory whose returned cmd.Run() succeeds (so multi-call
// methods like Up() — which now invokes `create` then `start` — don't
// short-circuit on a non-existent WorkDir's chdir failure).
//
// Takes a *testing.T so we can use t.TempDir() for an always-existing
// WorkDir. The Dir assertion in TestEveryMethodPropagatesWorkDirAndEnv
// uses the same value rather than hard-coding a path.
func runnerForWiring(t *testing.T, rec *recorder) *ComposeRunner {
	t.Helper()
	return &ComposeRunner{
		ProjectName:  "canton-test",
		ComposeFiles: []string{"compose.yaml", "overlay.yaml"},
		EnvFiles:     []string{"compose.env", "env/common.env"},
		Env:          []string{"PARTY_HINT=test-localparty-1", "DOCKER_NETWORK=test"},
		WorkDir:      t.TempDir(),
		commandFn:    rec.factory,
	}
}

func argvOf(cmd *exec.Cmd) []string { return cmd.Args }

func assertArgvContains(t *testing.T, got []string, want []string, label string) {
	t.Helper()
	g := strings.Join(got, " ")
	for _, w := range want {
		if !strings.Contains(g, w) {
			t.Errorf("%s argv missing %q\nfull argv: %v", label, w, got)
		}
	}
}

// --- wiring: argv shape + propagation ------------------------------------

func TestUpArgvIncludesProjectFilesAndEnvFiles(t *testing.T) {
	rec := &recorder{}
	c := runnerForWiring(t, rec)
	_ = c.Up(context.Background())

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(rec.calls))
	}
	argv := argvOf(rec.calls[0])
	assertArgvContains(t, argv, []string{
		"docker", "compose",
		"-p", "canton-test",
		"-f", "compose.yaml",
		"-f", "overlay.yaml",
		"--env-file", "compose.env",
		"--env-file", "env/common.env",
		"up", "-d",
	}, "Up")

	// -f order matters for "later overrides earlier" semantics.
	if i, j := indexOf(argv, "compose.yaml"), indexOf(argv, "overlay.yaml"); i > j {
		t.Errorf("compose files out of order: %v", argv)
	}
}

func TestEveryMethodPropagatesWorkDirAndEnv(t *testing.T) {
	cases := []struct {
		name string
		run  func(c *ComposeRunner)
	}{
		{"Up", func(c *ComposeRunner) { _ = c.Up(context.Background()) }},
		{"Down", func(c *ComposeRunner) { _ = c.Down(context.Background()) }},
		{"Restart", func(c *ComposeRunner) { _ = c.Restart(context.Background()) }},
	}
	// healthSnapshot / Endpoints / DiscoverPort require .Output(), which
	// invokes the underlying command. Those are covered by the
	// scriptedRecorder tests below, which also assert Dir/Env there.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			c := runnerForWiring(t, rec)
			tc.run(c)

			if len(rec.calls) != 1 {
				t.Fatalf("%s: expected 1 call, got %d", tc.name, len(rec.calls))
			}
			cmd := rec.calls[0]
			if cmd.Dir != c.WorkDir {
				t.Errorf("%s did not set cmd.Dir to runner WorkDir: got %q want %q",
					tc.name, cmd.Dir, c.WorkDir)
			}
			if !reflect.DeepEqual(cmd.Env, []string{
				"PARTY_HINT=test-localparty-1", "DOCKER_NETWORK=test",
			}) {
				t.Errorf("%s did not set cmd.Env: got %v", tc.name, cmd.Env)
			}
		})
	}
}

func TestDownArgvShape(t *testing.T) {
	rec := &recorder{}
	c := runnerForWiring(t, rec)
	_ = c.Down(context.Background())

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(rec.calls))
	}
	assertArgvContains(t, argvOf(rec.calls[0]), []string{
		"docker", "compose",
		"-p", "canton-test",
		"down", "--volumes", "--remove-orphans",
	}, "Down")
}

func TestRestartArgvShape(t *testing.T) {
	rec := &recorder{}
	c := runnerForWiring(t, rec)
	_ = c.Restart(context.Background())

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(rec.calls))
	}
	assertArgvContains(t, argvOf(rec.calls[0]), []string{
		"docker", "compose",
		"-p", "canton-test",
		"-f", "compose.yaml",
		"-f", "overlay.yaml",
		"--env-file", "compose.env",
		"--env-file", "env/common.env",
		"restart",
	}, "Restart")
}

func TestRestartArgvWithServices(t *testing.T) {
	rec := &recorder{}
	c := runnerForWiring(t, rec)
	_ = c.Restart(context.Background(), "canton", "splice")

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(rec.calls))
	}
	argv := argvOf(rec.calls[0])
	assertArgvContains(t, argv, []string{"restart", "canton", "splice"}, "Restart(services)")

	// Service args must follow "restart" in order.
	ri := indexOf(argv, "restart")
	if ri < 0 || ri+2 >= len(argv) {
		t.Fatalf("restart not found or not enough trailing args: %v", argv)
	}
	if argv[ri+1] != "canton" || argv[ri+2] != "splice" {
		t.Errorf("service args out of order: got %v after 'restart'", argv[ri+1:])
	}
}

// --- behaviour: classifyHealth parser ------------------------------------

func TestClassifyHealth(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantReady bool
		wantFatal string
	}{
		{
			name:      "all_healthy",
			raw:       "canton\trunning\thealthy\nsplice\trunning\thealthy\nnginx\trunning\thealthy\n",
			wantReady: true,
		},
		{
			name:      "running_without_healthcheck_counts_as_ready",
			raw:       "canton\trunning\thealthy\npostgres\trunning\t\n",
			wantReady: true,
		},
		{
			name:      "all_running_no_healthcheck",
			raw:       "a\trunning\t\nb\trunning\t\n",
			wantReady: true,
		},
		{
			name:      "starting_keeps_polling",
			raw:       "canton\trunning\thealthy\nsplice\trunning\tstarting\n",
			wantReady: false,
		},
		{
			// Splice's containers report unhealthy mid-onboarding;
			// the 15-min WaitForHealthy timeout is the actual gate.
			// classifyHealth should keep polling, not fail fast.
			name:      "unhealthy_keeps_polling",
			raw:       "canton\trunning\thealthy\nsplice\trunning\tunhealthy\n",
			wantReady: false,
		},
		{
			// Mirror case: an unhealthy snapshot should not lock the
			// poller into a fatal state — once the service recovers,
			// the next snapshot lands in the healthy bucket and the
			// poller succeeds.
			name:      "unhealthy_recovers_to_healthy",
			raw:       "canton\trunning\thealthy\nsplice\trunning\thealthy\n",
			wantReady: true,
		},
		{
			name:      "exited_is_fatal",
			raw:       "canton\trunning\thealthy\npostgres\texited\t\n",
			wantFatal: `service "postgres" is in state "exited"`,
		},
		{
			name:      "dead_is_fatal",
			raw:       "canton\tdead\t\n",
			wantFatal: `service "canton" is in state "dead"`,
		},
		{
			name:      "no_services_yet",
			raw:       "",
			wantReady: false,
		},
		{
			name:      "blank_lines_ignored",
			raw:       "\n\ncanton\trunning\thealthy\n\n",
			wantReady: true,
		},
		{
			name:      "created_state_keeps_polling",
			raw:       "canton\tcreated\t\n",
			wantReady: false,
		},
		{
			name:      "restarting_keeps_polling",
			raw:       "canton\trestarting\t\n",
			wantReady: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, fatal := classifyHealth([]byte(tt.raw))
			if tt.wantFatal != "" {
				if fatal == nil {
					t.Fatalf("expected fatal error containing %q, got nil; ready=%v", tt.wantFatal, ready)
				}
				if !strings.Contains(fatal.Error(), tt.wantFatal) {
					t.Errorf("fatal error mismatch:\n  got:  %v\n  want: %s", fatal, tt.wantFatal)
				}
				return
			}
			if fatal != nil {
				t.Fatalf("unexpected fatal: %v", fatal)
			}
			if ready != tt.wantReady {
				t.Errorf("ready: got %v, want %v", ready, tt.wantReady)
			}
		})
	}
}

// --- behaviour: WaitForHealthy + DiscoverPort + Endpoints via /bin/sh -----

func skipIfNoShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available; behaviour tests skipped on this platform")
	}
}

func TestWaitForHealthyReturnsOnFatal(t *testing.T) {
	skipIfNoShell(t)
	// Use a genuinely fatal state — exited — since unhealthy is no
	// longer fatal (Splice flips unhealthy briefly during onboarding;
	// the 15-min WaitForHealthy timeout is the real gate for that).
	rec := &scriptedRecorder{
		script: func(args []string) (string, int) {
			return "canton\trunning\thealthy\npostgres\texited\t\n", 0
		},
	}
	c := &ComposeRunner{
		ProjectName: "p", WorkDir: ".",
		commandFn: rec.factory,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.WaitForHealthy(ctx)
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("expected exited fatal, got %v", err)
	}
}

// TestWaitForHealthyRecoversFromTransientUnhealthy proves the
// behaviour: an unhealthy snapshot must not terminate the poller.
// Splice routinely reports unhealthy during
// onboarding, then settles to healthy. We script two ps calls in
// sequence: the first returns unhealthy, the second healthy. The
// poller must keep going past the first and succeed on the second.
func TestWaitForHealthyRecoversFromTransientUnhealthy(t *testing.T) {
	skipIfNoShell(t)
	calls := 0
	rec := &scriptedRecorder{
		script: func(args []string) (string, int) {
			calls++
			if calls == 1 {
				return "canton\trunning\thealthy\nsplice\trunning\tunhealthy\n", 0
			}
			return "canton\trunning\thealthy\nsplice\trunning\thealthy\n", 0
		},
	}
	c := &ComposeRunner{
		ProjectName: "p", WorkDir: ".",
		commandFn: rec.factory,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.WaitForHealthy(ctx); err != nil {
		t.Fatalf("expected success after transient unhealthy, got %v", err)
	}
	if calls < 2 {
		t.Errorf("expected at least 2 polls (unhealthy → healthy), got %d", calls)
	}
}

func TestWaitForHealthySucceedsWhenAllHealthy(t *testing.T) {
	skipIfNoShell(t)
	rec := &scriptedRecorder{
		script: func(args []string) (string, int) {
			return "canton\trunning\thealthy\nsplice\trunning\thealthy\n", 0
		},
	}
	c := &ComposeRunner{
		ProjectName: "p", WorkDir: ".",
		commandFn: rec.factory,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitForHealthy(ctx); err != nil {
		t.Fatalf("WaitForHealthy: %v", err)
	}
}

func TestWaitForHealthyTimesOut(t *testing.T) {
	skipIfNoShell(t)
	rec := &scriptedRecorder{
		script: func(args []string) (string, int) {
			return "canton\trunning\tstarting\n", 0
		},
	}
	c := &ComposeRunner{
		ProjectName: "p", WorkDir: ".",
		commandFn: rec.factory,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.WaitForHealthy(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscoverPortParsesIPv4AndIPv6(t *testing.T) {
	skipIfNoShell(t)
	cases := []struct {
		name   string
		stdout string
		want   int
	}{
		{"ipv4", "0.0.0.0:54321\n", 54321},
		{"ipv6", "[::]:54322\n", 54322},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &scriptedRecorder{
				script: func(args []string) (string, int) { return tc.stdout, 0 },
			}
			c := &ComposeRunner{
				ProjectName: "p", WorkDir: ".",
				commandFn: rec.factory,
			}
			got, err := c.DiscoverPort(context.Background(), "canton", 4001)
			if err != nil {
				t.Fatalf("DiscoverPort: %v", err)
			}
			if got != tc.want {
				t.Errorf("got port %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEndpointsParsesNamePublishersPairs(t *testing.T) {
	skipIfNoShell(t)
	rec := &scriptedRecorder{
		script: func(args []string) (string, int) {
			// Tab-separated: matches the {{.Name}}\t{{.Publishers}}
			// format the runner asks for. The nginx line carries TWO
			// comma-separated publishers to verify multi-publisher
			// services land in one value rather than being truncated.
			return "canton\t0.0.0.0:54321->4001/tcp\n" +
				"nginx\t0.0.0.0:60001->2000/tcp, 0.0.0.0:60002->3000/tcp\n", 0
		},
	}
	c := &ComposeRunner{
		ProjectName: "p", WorkDir: ".",
		commandFn: rec.factory,
	}
	got := c.Endpoints(context.Background())
	want := map[string]string{
		"canton": "0.0.0.0:54321->4001/tcp",
		"nginx":  "0.0.0.0:60001->2000/tcp, 0.0.0.0:60002->3000/tcp",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Endpoints mismatch:\n  got:  %v\n  want: %v", sortKeys(got), sortKeys(want))
	}
}

func TestPsUsesComposeRunnerCommandSeam(t *testing.T) {
	skipIfNoShell(t)
	wantOut := "demo-canton\trunning\thealthy\tsplice/canton:0.6.4\t4400->4400/tcp\n"
	rec := &scriptedRecorder{
		script: func(args []string) (string, int) {
			return wantOut, 0
		},
	}
	c := &ComposeRunner{
		ProjectName: "canton-demo",
		WorkDir:     t.TempDir(),
		Env:         []string{"DOCKER_HOST=unix:///tmp/docker.sock"},
		commandFn:   rec.factory,
	}
	out, err := c.Ps(context.Background())
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	if string(out) != wantOut {
		t.Errorf("Ps output = %q, want %q", string(out), wantOut)
	}
	if len(rec.argvs) != 1 {
		t.Fatalf("expected 1 docker call, got %d", len(rec.argvs))
	}
	wantArgv := []string{
		"docker", "compose", "-p", "canton-demo", "ps", "--all",
		"--format", "{{.Name}}\t{{.State}}\t{{.Health}}\t{{.Image}}\t{{.Publishers}}",
	}
	if !reflect.DeepEqual(rec.argvs[0], wantArgv) {
		t.Errorf("argv mismatch:\n  got:  %v\n  want: %v", rec.argvs[0], wantArgv)
	}
	if rec.calls[0].Dir != c.WorkDir {
		t.Errorf("cmd.Dir = %q, want %q", rec.calls[0].Dir, c.WorkDir)
	}
	if !reflect.DeepEqual(rec.calls[0].Env, []string{"DOCKER_HOST=unix:///tmp/docker.sock"}) {
		t.Errorf("cmd.Env = %v", rec.calls[0].Env)
	}
}

// Confirm scripted commands also receive WorkDir/Env — covers the
// previously buggy Endpoints / Down paths end-to-end.
func TestScriptedCommandsAlsoPropagateDirAndEnv(t *testing.T) {
	skipIfNoShell(t)
	rec := &scriptedRecorder{
		script: func(args []string) (string, int) { return "", 0 },
	}
	c := &ComposeRunner{
		ProjectName: "p",
		WorkDir:     "/tmp/splice-cache",
		Env:         []string{"PARTY_HINT=x"},
		commandFn:   rec.factory,
	}
	_, _ = c.healthSnapshot(context.Background())
	_ = c.Endpoints(context.Background())
	_, _ = c.DiscoverPort(context.Background(), "svc", 1234)
	if len(rec.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(rec.calls))
	}
	for i, cmd := range rec.calls {
		if cmd.Dir != "/tmp/splice-cache" {
			t.Errorf("call %d: cmd.Dir = %q, want /tmp/splice-cache", i, cmd.Dir)
		}
		if !reflect.DeepEqual(cmd.Env, []string{"PARTY_HINT=x"}) {
			t.Errorf("call %d: cmd.Env = %v, want [PARTY_HINT=x]", i, cmd.Env)
		}
	}
}

// --- helpers -------------------------------------------------------------

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TestAllBlockersAreSpliceStarting locks in the narrow gate for the
// out-of-band readyz fallback: it fires ONLY when every non-ready
// service is a `*-splice`-named container in running/starting state.
// The V2 alpha's `-dev` splice image ships a broken in-container
// HEALTHCHECK probe so docker stays at `starting` forever; the
// fallback lets WaitForHealthy probe `/api/validator/readyz` from
// outside and proceed when the validator actually responds.
//
// The gate has to be narrow: any other non-ready service (canton,
// postgres, nginx, wallet) must keep the fallback OFF so a real
// failure elsewhere doesn't get masked.
func TestAllBlockersAreSpliceStarting(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "all_healthy_no_blockers",
			raw:  "v2-canton\trunning\thealthy\nv2-splice\trunning\thealthy\n",
			want: false, // no blockers at all — caller already accepted; this returns false
		},
		{
			name: "only_splice_starting_fires",
			raw:  "v2-canton\trunning\thealthy\nv2-postgres\trunning\thealthy\nv2-splice\trunning\tstarting\n",
			want: true,
		},
		{
			name: "splice_starting_plus_other_starting_does_not_fire",
			raw:  "v2-canton\trunning\thealthy\nv2-nginx\trunning\tstarting\nv2-splice\trunning\tstarting\n",
			want: false,
		},
		{
			name: "splice_unhealthy_does_not_fire",
			raw:  "v2-canton\trunning\thealthy\nv2-splice\trunning\tunhealthy\n",
			want: false,
		},
		{
			name: "non_splice_starting_does_not_fire",
			raw:  "v2-canton\trunning\tstarting\nv2-splice\trunning\thealthy\n",
			want: false,
		},
		{
			name: "empty_input_does_not_fire",
			raw:  "",
			want: false,
		},
	}
	c := &ComposeRunner{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := c.allBlockersAreSpliceStarting([]byte(tc.raw))
			if got != tc.want {
				t.Fatalf("got %v, want %v\nraw=%q", got, tc.want, tc.raw)
			}
		})
	}
}

// TestSpliceContainerName covers the tiny helper that finds the
// `*-splice` container name in a ps snapshot — the input to the
// `docker exec` probe.
func TestSpliceContainerName(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "finds_first_splice", raw: "v2-canton\trunning\thealthy\nv2-splice\trunning\tstarting\n", want: "v2-splice"},
		{name: "different_project_prefix", raw: "obs-canton\trunning\thealthy\nobs-splice\trunning\tstarting\n", want: "obs-splice"},
		{name: "no_splice_returns_empty", raw: "canton\trunning\thealthy\n", want: ""},
		{name: "empty_input_returns_empty", raw: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := spliceContainerName([]byte(tc.raw))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func sortKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k, v := range m {
		ks = append(ks, k+"="+v)
	}
	sort.Strings(ks)
	return ks
}
