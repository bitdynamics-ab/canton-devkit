package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

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
