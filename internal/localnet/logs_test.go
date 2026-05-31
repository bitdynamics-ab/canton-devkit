package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestRunLogs_ConstructsDockerComposeLogsCommand(t *testing.T) {
	projectDir := seedLogsInstance(t, "logs-command")

	var gotArgs []string
	var gotDir string
	var gotEnv []string
	var out, errBuf bytes.Buffer
	code := RunLogs(context.Background(), &out, &errBuf, &LogsOptions{
		Name:     "logs-command",
		Follow:   true,
		Tail:     "all",
		Since:    "10m",
		Services: []string{"canton", "splice"},
		RunFn: func(_ context.Context, args []string, dir string, env []string, _ io.Writer, _ io.Writer) error {
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			gotEnv = append([]string(nil), env...)
			return nil
		},
	})
	if code != ExitSuccess {
		t.Fatalf("RunLogs = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}

	wantArgs := []string{
		"compose", "-p", "canton-logs-command",
		"-f", "compose.yaml",
		"-f", "/tmp/logs-command-overlay.yaml",
		"--env-file", "compose.env",
		"--env-file", "env/common.env",
		"logs", "--follow", "--tail", "all", "--since", "10m", "canton", "splice",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v\nwant %#v", gotArgs, wantArgs)
	}
	if gotDir != projectDir {
		t.Fatalf("dir = %q, want %q", gotDir, projectDir)
	}
	for _, want := range []string{"LOCALNET_DIR=" + projectDir, "IMAGE_TAG=0.6.4", "DOCKER_NETWORK=logs-command"} {
		if !containsEnv(gotEnv, want) {
			t.Fatalf("env missing %q in %#v", want, gotEnv)
		}
	}
}

func TestRunLogs_PropagatesDockerFailure(t *testing.T) {
	seedLogsInstance(t, "logs-fail")

	var out, errBuf bytes.Buffer
	code := RunLogs(context.Background(), &out, &errBuf, &LogsOptions{
		Name: "logs-fail",
		Tail: "100",
		RunFn: func(context.Context, []string, string, []string, io.Writer, io.Writer) error {
			return errors.New("daemon unavailable")
		},
	})
	if code != ExitRuntimeFailure {
		t.Fatalf("RunLogs failure = %d, want ExitRuntimeFailure", code)
	}
	if !strings.Contains(errBuf.String(), "docker compose logs: daemon unavailable") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}

func TestRunLogs_MissingInstanceIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out, errBuf bytes.Buffer
	code := RunLogs(context.Background(), &out, &errBuf, &LogsOptions{Name: "missing", Tail: "100"})
	if code != ExitUserError {
		t.Fatalf("RunLogs missing = %d, want ExitUserError; stdout=%q stderr=%q", code, out.String(), errBuf.String())
	}
}

func seedLogsInstance(t *testing.T, name string) string {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	projectDir := t.TempDir()
	state := registry.NewState(name, "0.6.4")
	state.ComposeProject = "canton-" + name
	state.ComposeFiles = []string{"compose.yaml", "/tmp/" + name + "-overlay.yaml"}
	state.ProjectDir = projectDir
	state.DataDir = registry.DataDirFor(name)
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}
	return projectDir
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
