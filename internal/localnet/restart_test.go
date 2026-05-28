package localnet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// stubProjectContainers swaps the listProjectContainers seam for the
// duration of a test, returning the given services as live compose
// containers. Restores the real containers.List on cleanup.
func stubProjectContainers(t *testing.T, services ...string) {
	t.Helper()
	prev := listProjectContainers
	t.Cleanup(func() { listProjectContainers = prev })
	infos := make([]containers.Info, 0, len(services))
	for _, s := range services {
		infos = append(infos, containers.Info{Service: s, State: "running"})
	}
	listProjectContainers = func(context.Context, string) ([]containers.Info, error) {
		return infos, nil
	}
}

// restarterFn adapts inline functions to composeRestarter so each
// test can script the Restart + WaitForHealthy outcomes.
type restarterFn struct {
	restart func(context.Context, ...string) error
	wait    func(context.Context) error
}

func (r restarterFn) Restart(ctx context.Context, services ...string) error {
	return r.restart(ctx, services...)
}
func (r restarterFn) WaitForHealthy(ctx context.Context) error { return r.wait(ctx) }

func okRestarter(restartCalled *bool, gotServices *[]string) func(string, []string, []string, []string, string, io.Writer) composeRestarter {
	return func(string, []string, []string, []string, string, io.Writer) composeRestarter {
		return restarterFn{
			restart: func(_ context.Context, svc ...string) error {
				if restartCalled != nil {
					*restartCalled = true
				}
				if gotServices != nil {
					*gotServices = svc
				}
				return nil
			},
			wait: func(context.Context) error { return nil },
		}
	}
}

func TestRunRestart_HappyPath(t *testing.T) {
	seedRunningInstance(t, "rs-happy")

	called := false
	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name:      "rs-happy",
		NewRunner: okRestarter(&called, nil),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRestart = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if !called {
		t.Error("Restart was not invoked")
	}
	// Status persists as running.
	st, err := registry.Read("rs-happy")
	if err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if st.Status != registry.StatusRunning {
		t.Errorf("status = %q, want running", st.Status)
	}
}

func TestRunRestart_ServiceScoped(t *testing.T) {
	seedRunningInstance(t, "rs-svc")

	var got []string
	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name:      "rs-svc",
		Services:  []string{"canton", "splice"},
		NewRunner: okRestarter(nil, &got),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRestart = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if len(got) != 2 || got[0] != "canton" || got[1] != "splice" {
		t.Errorf("services passed to Restart = %v, want [canton splice]", got)
	}
}

func TestRunRestart_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name:      "ghost",
		NewRunner: okRestarter(nil, nil),
	})
	if code != ExitUserError {
		t.Fatalf("RunRestart = %d, want ExitUserError", code)
	}
	if !bytes.Contains(errBuf.Bytes(), []byte("localnet up")) {
		t.Errorf("expected an 'up first' hint; stderr=%q", errBuf.String())
	}
}

func TestRunRestart_ComposeFailure(t *testing.T) {
	seedRunningInstance(t, "rs-fail")

	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name: "rs-fail",
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeRestarter {
			return restarterFn{
				restart: func(context.Context, ...string) error { return errors.New("boom") },
				wait:    func(context.Context) error { return nil },
			}
		},
	})
	if code != ExitRuntimeFailure {
		t.Fatalf("RunRestart = %d, want ExitRuntimeFailure", code)
	}
}

func TestRunRestart_ReadinessTimeoutMarksPartial(t *testing.T) {
	seedRunningInstance(t, "rs-unhealthy")

	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name: "rs-unhealthy",
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeRestarter {
			return restarterFn{
				restart: func(context.Context, ...string) error { return nil },
				wait:    func(context.Context) error { return errors.New("never healthy") },
			}
		},
	})
	if code != ExitTimeout {
		t.Fatalf("RunRestart = %d, want ExitTimeout", code)
	}
	st, _ := registry.Read("rs-unhealthy")
	if st.Status != registry.StatusPartial {
		t.Errorf("status = %q, want partial after readiness failure", st.Status)
	}
}

func TestRunRestart_UnknownServiceRejected(t *testing.T) {
	seedRunningInstance(t, "rs-typo")
	stubProjectContainers(t, "canton", "splice", "postgres")

	restartCalled := false
	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name:     "rs-typo",
		Services: []string{"cantn"}, // typo of "canton"
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeRestarter {
			return restarterFn{
				restart: func(context.Context, ...string) error { restartCalled = true; return nil },
				wait:    func(context.Context) error { return nil },
			}
		},
	})
	if code != ExitUserError {
		t.Fatalf("RunRestart = %d, want ExitUserError; stderr=%q", code, errBuf.String())
	}
	if restartCalled {
		t.Error("compose Restart must not run when a service name is unknown")
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, `unknown service "cantn"`) {
		t.Errorf("expected the typo'd service in the error; stderr=%q", stderr)
	}
	// Known services listed, sorted, for the user to copy.
	if !strings.Contains(stderr, "Known services: canton, postgres, splice") {
		t.Errorf("expected sorted known-services hint; stderr=%q", stderr)
	}
}

func TestRunRestart_KnownServiceAccepted(t *testing.T) {
	seedRunningInstance(t, "rs-known")
	stubProjectContainers(t, "canton", "splice")

	var got []string
	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name:      "rs-known",
		Services:  []string{"canton"},
		NewRunner: okRestarter(nil, &got),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRestart = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if len(got) != 1 || got[0] != "canton" {
		t.Errorf("services passed to Restart = %v, want [canton]", got)
	}
}

func TestRunRestart_PortRecapturePersisted(t *testing.T) {
	seedRunningInstance(t, "rs-ports")

	// Seed stale ports into the registry.
	st, _ := registry.Read("rs-ports")
	st.Ports = map[string]int{
		"participant_ledger_app-user": 11111, // stale
		"app_user_ui":                 2000,  // fixed UI port, should survive
	}
	if err := registry.Write(st); err != nil {
		t.Fatalf("seed ports: %v", err)
	}

	// Stub composePortCmd so CaptureCantonPorts returns refreshed
	// values without requiring a running docker daemon.
	prev := composePortCmd
	t.Cleanup(func() { composePortCmd = prev })
	composePortCmd = func(_ context.Context, project, service string, internal int) ([]byte, error) {
		// Return a new host port = internal + 50000 for every known
		// Canton internal port.
		return []byte(fmt.Sprintf("0.0.0.0:%d\n", internal+50000)), nil
	}

	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name:      "rs-ports",
		NewRunner: okRestarter(nil, nil),
	})
	if code != ExitSuccess {
		t.Fatalf("RunRestart = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}

	after, err := registry.Read("rs-ports")
	if err != nil {
		t.Fatalf("read after restart: %v", err)
	}

	// The stale ledger port should be replaced by the refreshed value.
	// Internal port 2901 → host 52901.
	if got := after.Ports["participant_ledger_app-user"]; got != 52901 {
		t.Errorf("participant_ledger_app-user = %d, want 52901 (refreshed)", got)
	}
	// A fixed UI port that CaptureCantonPorts doesn't touch should survive.
	if got := after.Ports["app_user_ui"]; got != 2000 {
		t.Errorf("app_user_ui = %d, want 2000 (preserved)", got)
	}
	// Spot-check another Canton port was captured.
	if got := after.Ports["participant_admin_sv"]; got != 54902 {
		t.Errorf("participant_admin_sv = %d, want 54902", got)
	}
}

func TestRunRestart_NoWaitSkipsHealthCheck(t *testing.T) {
	seedRunningInstance(t, "rs-nowait")

	waitCalled := false
	var out, errBuf bytes.Buffer
	code := RunRestart(context.Background(), &out, &errBuf, &RestartOptions{
		Name:     "rs-nowait",
		SkipWait: true,
		NewRunner: func(string, []string, []string, []string, string, io.Writer) composeRestarter {
			return restarterFn{
				restart: func(context.Context, ...string) error { return nil },
				wait:    func(context.Context) error { waitCalled = true; return nil },
			}
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunRestart = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if waitCalled {
		t.Error("--no-wait must not invoke WaitForHealthy")
	}
}
