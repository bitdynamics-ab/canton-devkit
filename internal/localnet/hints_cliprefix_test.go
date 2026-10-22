package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func forEachCLIPrefix(t *testing.T, fn func(t *testing.T, prefix, other string)) {
	for _, tc := range []struct{ set, prefix, other string }{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			if tc.set != "" {
				t.Cleanup(cliprefix.Set(tc.set))
			}
			fn(t, tc.prefix, tc.other)
		})
	}
}

func assertCLIHints(t *testing.T, got string, want []string, other string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, other) {
		t.Errorf("output must not mention %q:\n%s", other, got)
	}
	if strings.Contains(got, "`localnet ") {
		t.Errorf("output has a prefix-less `localnet ...` hint:\n%s", got)
	}
}

func TestRunStop_HintsFollowCLIPrefix(t *testing.T) {
	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		seedRunningInstance(t, "demo")
		var out, errb bytes.Buffer
		if exit := RunStop(context.Background(), &out, &errb, &StopOptions{Name: "ghost"}); exit != ExitUserError {
			t.Fatalf("unknown instance exit=%d, want ExitUserError", exit)
		}
		assertCLIHints(t, errb.String(), []string{"Run `" + prefix + " up ghost` first."}, other)

		s, _ := registry.Read("demo")
		s.Status = registry.StatusFailed
		if err := registry.Write(s); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		errb.Reset()
		if exit := RunStop(context.Background(), &out, &errb, &StopOptions{Name: "demo"}); exit != ExitUserError {
			t.Fatalf("failed instance exit=%d, want ExitUserError", exit)
		}
		assertCLIHints(t, errb.String(), []string{"Use `" + prefix + " down demo` to tear it down."}, other)

		s.Status = registry.StatusRunning
		if err := registry.Write(s); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		errb.Reset()
		opts := &StopOptions{Name: "demo", NewRunner: fakeStopper(func(context.Context) error { return nil })}
		if exit := RunStop(context.Background(), &out, &errb, opts); exit != ExitSuccess {
			t.Fatalf("stop exit=%d, stderr=%s", exit, errb.String())
		}
		assertCLIHints(t, out.String(), []string{"Run `" + prefix + " start demo` to resume."}, other)
	})
}

func TestRunStart_HintsFollowCLIPrefix(t *testing.T) {
	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		seedRunningInstance(t, "demo")
		s, _ := registry.Read("demo")
		s.Status = registry.StatusPaused
		if err := registry.Write(s); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if exit := RunStart(context.Background(), NewTextProgress(&out, &errb), &out, &errb, &StartOptions{Name: "demo"}); exit != ExitUserError {
			t.Fatalf("paused start exit=%d, want ExitUserError", exit)
		}
		assertCLIHints(t, errb.String(), []string{
			"Run `" + prefix + " resume demo` (or `" + prefix + " unpause demo`) to continue it.",
		}, other)

		s.Status = registry.StatusStopped
		if err := registry.Write(s); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		errb.Reset()
		opts := &StartOptions{
			Name: "demo",
			listContainers: func(context.Context, string) ([]containers.Info, error) {
				return []containers.Info{{Service: "canton", State: "exited"}}, nil
			},
			NewRunner: fakeStarter(starterFn{
				start: func(context.Context) error { return errors.New("no such container") },
				wait:  func(context.Context) error { return nil },
			}),
		}
		if exit := RunStart(context.Background(), NewTextProgress(&out, &errb), &out, &errb, opts); exit != ExitRuntimeFailure {
			t.Fatalf("failed fast start exit=%d, want ExitRuntimeFailure", exit)
		}
		assertCLIHints(t, errb.String(), []string{"run `" + prefix + " up demo` instead."}, other)
	})
}

func TestRunDown_FailureHintFollowsCLIPrefix(t *testing.T) {
	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		seedRunningInstance(t, "demo")
		var out, errb bytes.Buffer
		code := RunDown(context.Background(), &out, &errb, &DownOptions{
			Name: "demo",
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeDowner {
				return downerFn(func(context.Context) error { return errors.New("daemon gone") })
			},
		})
		if code != ExitRuntimeFailure {
			t.Fatalf("RunDown exit=%d, want ExitRuntimeFailure", code)
		}
		assertCLIHints(t, errb.String(), []string{"Re-run `" + prefix + " down demo` once the host is healthy."}, other)
	})
}

func TestRunUp_RefusalHintsFollowCLIPrefix(t *testing.T) {
	upOpts := func(t *testing.T, name string) *UpOptions {
		return &UpOptions{
			Name:          name,
			Version:       splice.LatestAlias,
			SkipPreflight: true,
			FetchFn: func(context.Context, splice.Version, string, io.Writer) (string, error) {
				return t.TempDir(), nil
			},
			NewRunner: func(string, []string, []string, []string, string, io.Writer) composeOps {
				t.Fatal("compose must not run on a refused up")
				return nil
			},
		}
	}

	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
		running := registry.NewState("live", "0.6.4")
		running.Status = registry.StatusRunning
		running.Ports = map[string]int{"app_user_ui": 12345}
		if err := registry.Write(running); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if code := RunUp(context.Background(), &TextProgress{OutW: &out, ErrW: &errb}, upOpts(t, "live")); code != ExitUserError {
			t.Fatalf("up on a running instance = %d, want ExitUserError; stderr=%s", code, errb.String())
		}
		assertCLIHints(t, errb.String(), []string{
			"stop it first with `" + prefix + " down live`",
			"bounce it with `" + prefix + " restart live`",
		}, other)

		held, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held.Close() }()
		stopped := registry.NewState("busy", "0.6.4")
		stopped.Status = registry.StatusStopped
		stopped.Ports = map[string]int{"app_user_ui": held.Addr().(*net.TCPAddr).Port}
		if err := registry.Write(stopped); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		errb.Reset()
		if code := RunUp(context.Background(), &TextProgress{OutW: &out, ErrW: &errb}, upOpts(t, "busy")); code != ExitPreflightFail {
			t.Fatalf("up with a busy port = %d, want ExitPreflightFail; stderr=%s", code, errb.String())
		}
		assertCLIHints(t, errb.String(), []string{"re-run `" + prefix + " up busy`."}, other)
	})
}

func TestRunRemove_RunningRefusalFollowsCLIPrefix(t *testing.T) {
	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
		seedInstanceStatus(t, "live", registry.StatusRunning)
		var out, errb bytes.Buffer
		code := RunRemove(context.Background(), &out, &errb, &RemoveOptions{Name: "live", NewRunner: nilDowner()})
		if code != ExitUserError {
			t.Fatalf("RunRemove = %d, want ExitUserError", code)
		}
		assertCLIHints(t, errb.String(), []string{"refusing to remove. Run `" + prefix + " down live` first"}, other)
	})
}

func TestWritePreservingEdits_NoticeFollowsCLIPrefix(t *testing.T) {
	forEachCLIPrefix(t, func(t *testing.T, prefix, other string) {
		dest := filepath.Join(t.TempDir(), "overlay.yaml")
		if err := os.WriteFile(dest, []byte("edited: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var warn bytes.Buffer
		if err := writePreservingEdits(dest, []byte("edited: false\n"), &warn); err != nil {
			t.Fatal(err)
		}
		assertCLIHints(t, warn.String(), []string{"restore the default on the next `" + prefix + " up`)"}, other)
	})
}
