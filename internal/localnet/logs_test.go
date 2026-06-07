package localnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestRunLogs_ConstructsDockerComposeLogsCommand(t *testing.T) {
	seedLogsInstance(t, "logs-command")

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

	argsStr := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"compose -p canton-logs-command logs",
		"logs --follow --tail all --since 10m canton splice",
	} {
		if !strings.Contains(argsStr, want) {
			t.Fatalf("args missing %q in %v", want, gotArgs)
		}
	}
	for _, forbidden := range []string{"-f", "--env-file"} {
		for _, arg := range gotArgs {
			if arg == forbidden {
				t.Fatalf("args should not contain %q in %v", forbidden, gotArgs)
			}
		}
	}
	for _, arg := range gotArgs {
		if strings.Contains(arg, "overlay.env") {
			t.Fatalf("args should not contain overlay.env in %v", gotArgs)
		}
	}
	if gotDir != "" {
		t.Fatalf("dir = %q, want empty project-label lookup", gotDir)
	}
	if gotEnv != nil {
		t.Fatalf("env should be nil, got %d entries", len(gotEnv))
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

	// Write overlay.env so downstream commands can resolve env files.
	if _, err := WriteOverlayEnv(state.DataDir, map[string]string{
		"LOCALNET_DIR":   projectDir,
		"IMAGE_TAG":      "0.6.4",
		"DOCKER_NETWORK": name,
		"PARTY_HINT":     name + "-localparty-1",
	}); err != nil {
		t.Fatalf("WriteOverlayEnv: %v", err)
	}

	return projectDir
}
