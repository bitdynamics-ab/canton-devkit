package localnet

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestConfirmStopPrompt_AcceptsYes(t *testing.T) {
	for _, answer := range []string{"y\n", "Y\n", "yes\n", "  yes  \n"} {
		var errBuf bytes.Buffer
		ok, err := confirmStopPrompt(strings.NewReader(answer), &errBuf)("demo")
		if err != nil {
			t.Fatalf("answer %q: unexpected error %v", answer, err)
		}
		if !ok {
			t.Errorf("answer %q should confirm the teardown", answer)
		}
		if !strings.Contains(errBuf.String(), "Stop and remove demo? [y/N]") {
			t.Errorf("prompt not shown; got %q", errBuf.String())
		}
	}
}

func TestConfirmStopPrompt_DefaultsToNo(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "no\n", "", "maybe\n"} {
		var errBuf bytes.Buffer
		ok, err := confirmStopPrompt(strings.NewReader(answer), &errBuf)("demo")
		if err != nil {
			t.Fatalf("answer %q: unexpected error %v", answer, err)
		}
		if ok {
			t.Errorf("answer %q must not confirm the teardown", answer)
		}
	}
}

// A stdin that cannot answer a prompt must fail fast with a --force
// hint instead of prompting into the void. /dev/null is covered
// explicitly: it is a character device, so a mode-based TTY check
// mistakes it for a terminal, prompts, reads EOF, and silently reports
// the instance as kept.
func TestConfirmStopPrompt_NonInteractiveStdinErrors(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("temp stdin: %v", err)
	}
	defer func() { _ = regular.Close() }()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()

	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = pipeR.Close(); _ = pipeW.Close() }()

	for name, in := range map[string]*os.File{
		"redirected file": regular,
		"dev null":        devNull,
		"pipe":            pipeR,
	} {
		t.Run(name, func(t *testing.T) {
			var errBuf bytes.Buffer
			ok, err := confirmStopPrompt(in, &errBuf)("demo")
			if err == nil {
				t.Fatal("non-interactive stdin must not silently prompt")
			}
			if ok {
				t.Error("non-interactive stdin must not confirm")
			}
			if !strings.Contains(err.Error(), "--force") {
				t.Errorf("error should point at --force; got %q", err.Error())
			}
			if errBuf.Len() != 0 {
				t.Errorf("no prompt should be written to a non-interactive stdin; got %q", errBuf.String())
			}
		})
	}
}

// End to end through the cobra command: a running instance is offered
// for teardown rather than refused, and answering "n" leaves it alone.
func TestRemoveCommand_RunningDeclinedPreservesInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "live-cli", registry.StatusRunning)

	cmd := buildRemove()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs([]string{"live-cli"})

	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("declining the prompt should be a non-zero exit")
	}
	if !strings.Contains(errBuf.String(), "Stop and remove live-cli? [y/N]") {
		t.Errorf("a running instance should be offered for teardown; stderr=%q", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "refusing to remove") {
		t.Errorf("the CLI should prompt instead of refusing; stderr=%q", errBuf.String())
	}
	if _, err := registry.Read("live-cli"); err != nil {
		t.Errorf("declined remove must preserve the instance, got err=%v", err)
	}
}
