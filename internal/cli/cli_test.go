package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunShowsRootHelpWithoutArgs(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "test").Run(nil)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", out.String())
	}
	if err.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", err.String())
	}
}

func TestRunShowsVersion(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "1.2.3").Run([]string{"version"})

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if got := out.String(); got != "canton-devkit 1.2.3\n" {
		t.Fatalf("unexpected version output: %q", got)
	}
	if err.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", err.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "test").Run([]string{"bogus"})

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(err.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got %q", err.String())
	}
}

func TestRunLocalnetPlaceholderCommands(t *testing.T) {
	placeholders := []string{"down", "restart", "clean", "status", "logs"}
	for _, command := range placeholders {
		t.Run(command, func(t *testing.T) {
			var out bytes.Buffer
			var errBuf bytes.Buffer

			code := New(&out, &errBuf, "test").Run([]string{"localnet", command})

			if code != 0 {
				t.Fatalf("expected exit code 0, got %d", code)
			}
			if !strings.Contains(out.String(), "not implemented yet") {
				t.Fatalf("expected placeholder output, got %q", out.String())
			}
			if errBuf.Len() != 0 {
				t.Fatalf("expected no stderr output, got %q", errBuf.String())
			}
		})
	}
}

// TestRunIsArgvOnly documents the DPM-component invocation contract: when the
// binary is launched via a DPM component manifest with exec-args: ["localnet"],
// DPM prepends "localnet" and appends user-supplied args. The CLI core must
// dispatch correctly from that argv slice with no reliance on argv[0] or env.
func TestRunIsArgvOnly(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	// Mirrors DPM invocation: exec-args ["localnet"] + user args ["up", "--name", "foo"]
	// Dispatches to localnet up; may exit 0 (full success), 2 (no docker), or 4 (compose fail).
	code := New(&out, &errBuf, "test").Run([]string{"localnet", "up", "--name", "foo"})

	if code == 1 {
		t.Fatalf("exit code 1 means bad args — dispatch failed: stderr=%q", errBuf.String())
	}
	if !strings.Contains(out.String(), "Starting Canton LocalNet") {
		t.Fatalf("expected localnet up dispatch, got stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}

func TestRunRejectsUnknownLocalnetCommand(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "test").Run([]string{"localnet", "bogus"})

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(err.String(), "unknown localnet command") {
		t.Fatalf("expected unknown localnet command error, got %q", err.String())
	}
}
