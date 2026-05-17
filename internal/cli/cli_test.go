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

func TestRunLocalnetCommands(t *testing.T) {
	localnetSubcommands := []string{"up", "down", "restart", "clean", "status", "logs"}
	for _, command := range localnetSubcommands {
		t.Run(command, func(t *testing.T) {
			var out bytes.Buffer
			var err bytes.Buffer

			code := New(&out, &err, "test").Run([]string{"localnet", command})

			if code != 0 {
				t.Fatalf("expected exit code 0, got %d", code)
			}
			if !strings.Contains(out.String(), "not implemented yet") {
				t.Fatalf("expected placeholder output, got %q", out.String())
			}
			if err.Len() != 0 {
				t.Fatalf("expected no stderr output, got %q", err.String())
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
	var err bytes.Buffer

	// Mirrors DPM invocation: exec-args ["localnet"] + user args ["up", "--name", "foo"]
	code := New(&out, &err, "test").Run([]string{"localnet", "up", "--name", "foo"})

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(out.String(), "not implemented yet") {
		t.Fatalf("expected placeholder output, got %q", out.String())
	}
	if err.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", err.String())
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
