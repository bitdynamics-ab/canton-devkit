package localnet

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
)

// DoctorOptions captures `localnet doctor` flags. Reserved (currently
// no flags) so future options (e.g. --json) can be added without
// touching the Cobra builder.
type DoctorOptions struct{}

// RunDoctor runs every preflight check and prints a bug-report-friendly
// diagnostic to out. Exit codes:
//   - 0 if every check passes (warnings allowed)
//   - 2 if any check FAILs
//
// Output is deliberately plain text with a header so users can copy/paste
// into bug reports. The same checks back `localnet up`, so a green doctor
// run implies up should pass preflight.
func RunDoctor(ctx context.Context, out io.Writer, _ io.Writer, _ *DoctorOptions) int {
	writeHeader(out)

	// Same checks `localnet up` runs (Docker / Compose v2 / disk /
	// memory). Host TCP ports aren't preflight-checked anywhere — DevKit
	// allocates them ephemerally — so a green doctor implies `up` will
	// also pass preflight.
	report := docker.RunPreflight(ctx, docker.Options{
		MinDiskBytes:   10 * 1024 * 1024 * 1024, // 10 GB
		MinMemoryBytes: 4 * 1024 * 1024 * 1024,  // 4 GB
	})

	writeVersions(out, report)

	_, _ = fmt.Fprintln(out, "Checks:")
	report.Write(out)

	_, _ = fmt.Fprintln(out)
	if report.OK() {
		if report.HasWarnings() {
			_, _ = fmt.Fprintln(out, "Result: PASS (with warnings)")
		} else {
			_, _ = fmt.Fprintln(out, "Result: PASS")
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintln(out, "Result: FAIL")
	_, _ = fmt.Fprintln(out, "One or more checks failed. See remediation hints above.")
	return ExitPreflightFail
}

// writeHeader prints a short, copy-pasteable system summary suitable for
// inclusion at the top of a bug report.
func writeHeader(out io.Writer) {
	_, _ = fmt.Fprintln(out, "canton-devkit doctor")
	_, _ = fmt.Fprintln(out, "====================")
	_, _ = fmt.Fprintf(out, "Timestamp:     %s\n", time.Now().UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintf(out, "OS / Arch:     %s/%s\n", runtime.GOOS, runtime.GOARCH)
	_, _ = fmt.Fprintf(out, "Go runtime:    %s\n", runtime.Version())
	_, _ = fmt.Fprintf(out, "CPUs:          %d\n", runtime.NumCPU())
	_, _ = fmt.Fprintln(out)
}

// writeVersions prints the Docker daemon and Compose v2 versions in a
// dedicated, easy-to-copy block. We surface these here (instead of only
// inside the Checks section) so bug reports can lead with the versions
// without scanning the full check output.
//
// Unknown values print as "(not detected)" — that happens when the
// preceding preflight check failed (Docker CLI missing, daemon down,
// Compose v1).
func writeVersions(out io.Writer, report *docker.Report) {
	dockerV := report.DockerVersion
	if dockerV == "" {
		dockerV = "(not detected)"
	} else {
		dockerV = "v" + dockerV
	}
	composeV := report.ComposeVersion
	if composeV == "" {
		composeV = "(not detected)"
	} else if composeV[0] != 'v' {
		composeV = "v" + composeV
	}
	_, _ = fmt.Fprintln(out, "Versions:")
	_, _ = fmt.Fprintf(out, "  Docker:      %s\n", dockerV)
	_, _ = fmt.Fprintf(out, "  Compose v2:  %s\n", composeV)
	_, _ = fmt.Fprintln(out)
}
