package localnet

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestRunDoctorHeaderContents(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	// We can't predict whether all preflight checks pass on every CI host
	// (e.g. Windows in CI can't bind port 5432 if something else uses it).
	// Assert only on the bug-report header content + exit-code shape.
	var out bytes.Buffer
	var errBuf bytes.Buffer
	code := RunDoctor(context.Background(), &out, &errBuf, &DoctorOptions{})

	if code != ExitSuccess && code != ExitPreflightFail {
		t.Fatalf("expected exit 0 or 2, got %d", code)
	}

	body := out.String()
	mustContain := []string{
		"canton-devkit doctor",
		"Timestamp:",
		"OS / Arch:     " + runtime.GOOS + "/" + runtime.GOARCH,
		"Go runtime:    " + runtime.Version(),
		"Versions:",
		"Docker:",
		"Compose v2:",
		"Checks:",
		"Disk space",
		"Result:",
	}
	for _, s := range mustContain {
		if !strings.Contains(body, s) {
			t.Errorf("doctor output missing %q\n---\n%s\n---", s, body)
		}
	}
}

func TestRunDoctorExitCodeMatchesResultLine(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out bytes.Buffer
	var errBuf bytes.Buffer
	code := RunDoctor(context.Background(), &out, &errBuf, &DoctorOptions{})
	body := out.String()

	switch code {
	case ExitSuccess:
		if !strings.Contains(body, "Result: PASS") {
			t.Errorf("exit 0 but no PASS line:\n%s", body)
		}
	case ExitPreflightFail:
		if !strings.Contains(body, "Result: FAIL") {
			t.Errorf("exit 2 but no FAIL line:\n%s", body)
		}
	default:
		t.Fatalf("unexpected exit code %d", code)
	}
}
