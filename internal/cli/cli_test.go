package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunShowsRootHelpWithoutArgs(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "test", "").Run(nil)

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

	code := New(&out, &err, "1.2.3", "").Run([]string{"version"})

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

func TestRunShowsVersionWithCommit(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "1.2.3", "abc1234").Run([]string{"version"})

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if got := out.String(); got != "canton-devkit 1.2.3 (abc1234)\n" {
		t.Fatalf("unexpected version output: %q", got)
	}
	if err.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", err.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "test", "").Run([]string{"bogus"})

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(err.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got %q", err.String())
	}
}

// TestRunLocalnetRemove_RequiresTarget pins that `remove` (and its
// `clean` alias) invoked with no --name/--all exits non-zero with
// guidance rather than a placeholder message.
func TestRunLocalnetRemove_RequiresTarget(t *testing.T) {
	// Both the canonical verb and the alias must behave identically.
	for _, verb := range []string{"remove", "clean"} {
		t.Run(verb, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := New(&out, &errb, "test", "").Run([]string{"localnet", verb})
			if code == 0 {
				t.Fatalf("%s with no target should exit non-zero, got 0", verb)
			}
			if strings.Contains(out.String(), "not implemented yet") {
				t.Fatalf("%s should no longer be a placeholder; got %q", verb, out.String())
			}
			if !strings.Contains(errb.String(), "--name") && !strings.Contains(errb.String(), "--all") {
				t.Fatalf("%s should hint at --name/--all; stderr=%q", verb, errb.String())
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
// We assert on the dispatch shape rather than the final exit code:
// a real `localnet up` depends on whether Docker is available on this
// host, while `--help` short-circuits and is deterministic.
func TestRunIsArgvOnly(t *testing.T) {
	// Both the direct argv and the DPM argv (marker prepended via
	// exec-args) must dispatch identically into `localnet up`.
	for _, args := range [][]string{
		{"localnet", "up", "--help"},
		{"--via-dpm", "localnet", "up", "--help"},
	} {
		var out bytes.Buffer
		var err bytes.Buffer

		code := New(&out, &err, "test", "").Run(args)

		if code != 0 {
			t.Fatalf("args %v: expected exit 0 for --help, got %d (stderr=%q)", args, code, err.String())
		}
		if !strings.Contains(out.String(), "Splice LocalNet") {
			t.Fatalf("args %v: expected up help to mention Splice LocalNet, got %q", args, out.String())
		}
	}
}

func TestRunLocalnetDoctorDispatch(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	// Doctor exits 0 (PASS) or 2 (FAIL) depending on the host. Either is
	// fine for a dispatch test — we just need to confirm the doctor code
	// path ran (readiness line present).
	code := New(&out, &errBuf, "test", "").Run([]string{"localnet", "doctor"})

	if code != 0 && code != 2 {
		t.Fatalf("expected exit 0 or 2, got %d (stderr=%q)", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "Checking host readiness for Canton LocalNet") {
		t.Fatalf("expected doctor readiness output, got stdout=%q", out.String())
	}
}

func TestRunLocalnetDoctorHelp(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer

	code := New(&out, &errBuf, "test", "").Run([]string{"localnet", "doctor", "--help"})

	if code != 0 {
		t.Fatalf("expected exit 0 for help, got %d", code)
	}
	if !strings.Contains(out.String(), "preflight check") {
		t.Fatalf("expected doctor help text mentioning preflight, got %q", out.String())
	}
}

func TestRunRejectsUnknownLocalnetCommand(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	code := New(&out, &err, "test", "").Run([]string{"localnet", "bogus"})

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(err.String(), "unknown localnet command") {
		t.Fatalf("expected unknown localnet command error, got %q", err.String())
	}
}
