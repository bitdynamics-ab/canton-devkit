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

// TestRunLocalnetPlaceholderCommands covers the still-stubbed
// subcommands. `up`, `down`, `status`, `logs`, `list`, `creds` are real
// commands now (they validate flags and call into internal/localnet);
// `restart` and `clean` remain DisableFlagParsing stubs.
func TestRunLocalnetPlaceholderCommands(t *testing.T) {
	placeholders := []string{"restart", "clean"}
	for _, command := range placeholders {
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

// TestRunIsArgvOnly documents the DPM-component invocation contract:
// when the binary is launched via a DPM manifest with exec-args:
// ["localnet"], DPM prepends "localnet" and appends user-supplied args.
// The CLI core must dispatch correctly from that argv slice with no
// reliance on argv[0] or env.
//
// We assert on the dispatch shape rather than the final exit code,
// because `localnet up` now actually runs preflight + fetch + compose
// — its result depends on whether Docker is available on this host.
// `--help` short-circuits before any of that, so it's deterministic.
func TestRunIsArgvOnly(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "test").Run([]string{"localnet", "up", "--help"})

	if code != 0 {
		t.Fatalf("expected exit 0 for --help, got %d (stderr=%q)", code, err.String())
	}
	if !strings.Contains(out.String(), "Splice LocalNet") {
		t.Fatalf("expected up help to mention Splice LocalNet, got %q", out.String())
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
