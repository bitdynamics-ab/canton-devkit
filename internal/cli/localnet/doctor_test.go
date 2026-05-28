package localnet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
)

func installFakeDoctorProber(t *testing.T, fn func(ctx context.Context, opts docker.Options) *docker.Report) {
	t.Helper()
	prev := doctorCollectFn
	doctorCollectFn = func(ctx context.Context, _ localnet.DoctorOptions) (types.PreflightReport, error) {
		return localnet.PreflightReportFromDocker(fn(ctx, docker.Options{})), nil
	}
	t.Cleanup(func() { doctorCollectFn = prev })
}

// TestDoctor_AllPassExitsZero pins the happy-path UX: every check
// returns OK → exit 0 + brand-colored summary Box.
func TestDoctor_AllPassExitsZero(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker CLI", Status: docker.StatusOK, Detail: "v25.0.5"},
			{Name: "Docker Compose v2", Status: docker.StatusOK, Detail: "v2.30.1"},
			{Name: "Memory available", Status: docker.StatusOK, Detail: "24 GiB"},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	body := out.String()
	for _, want := range []string{"SYSTEM", "RESOURCES", "All checks passed", "Docker CLI", "v25.0.5"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, body)
		}
	}
}

// TestDoctor_AnyFailExitsPreflightCode is the contract that lets
// shell scripts gate on `dpm localnet doctor` before `up`:
//
//	dpm localnet doctor && dpm localnet up --name demo
//
// A FAIL must produce ExitPreflightFail (2), not 1 (generic).
func TestDoctor_AnyFailExitsPreflightCode(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker CLI", Status: docker.StatusOK},
			{Name: "Port 65535 free", Status: docker.StatusFail, Detail: "pid 88341",
				Remediation: "Stop the conflicting process\nkill 88341"},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-nil error for FAIL result")
	}
	var ece localnet.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if int(ece) != localnet.ExitPreflightFail {
		t.Errorf("exit code = %d, want ExitPreflightFail (%d)", int(ece), localnet.ExitPreflightFail)
	}
	// The remediation lines must be present in the rendered Box.
	if !strings.Contains(out.String(), "Stop the conflicting process") {
		t.Errorf("expected remediation in output, got %q", out.String())
	}
}

// TestDoctor_JSONShape pins the --format=json contract — same shape
// the Web UI handler (BIT-131 GET /api/doctor) will consume.
func TestDoctor_JSONShape(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker CLI", Status: docker.StatusOK, Detail: "v25.0.5"},
			{Name: "Memory available", Status: docker.StatusWarn, Detail: "6 GiB",
				Remediation: "Increase Docker Desktop memory to ≥ 8 GiB"},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--format=json"})
	_ = cmd.Execute() // exit 0 (no fails); ignore Exit-code

	var got types.PreflightReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if got.SchemaVersion != types.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, types.SchemaVersion)
	}
	if !got.OK {
		t.Error("OK = false, but no FAIL was present in the fake report")
	}
	// Find the Memory check and verify remediation came through.
	var memCheck *types.PreflightCheck
	for i, sec := range got.Sections {
		for j, c := range sec.Checks {
			if c.Label == "Memory available" {
				memCheck = &got.Sections[i].Checks[j]
			}
		}
	}
	if memCheck == nil {
		t.Fatalf("Memory check not found in %+v", got)
	}
	if memCheck.Result != "warn" {
		t.Errorf("Memory.Result = %q, want warn", memCheck.Result)
	}
	if len(memCheck.Remediation) == 0 || !strings.Contains(memCheck.Remediation[0], "Increase Docker") {
		t.Errorf("remediation not propagated: %+v", memCheck.Remediation)
	}
}

func TestDoctor_PassesVersionToCollector(t *testing.T) {
	prev := doctorCollectFn
	var got localnet.DoctorOptions
	doctorCollectFn = func(_ context.Context, opts localnet.DoctorOptions) (types.PreflightReport, error) {
		got = opts
		return localnet.PreflightReportFromDocker(&docker.Report{Results: []docker.CheckResult{
			{Name: "Docker CLI", Status: docker.StatusOK},
		}}), nil
	}
	t.Cleanup(func() { doctorCollectFn = prev })

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--format=json", "--version=0.6.4"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got.Version != "0.6.4" {
		t.Errorf("Version = %q, want 0.6.4", got.Version)
	}
}

// TestDoctor_CategoriserUpstreamNames is the BIT-123 review pin
// (reviewer flagged the previous synthetic-fixture test as hiding
// real bugs). Uses the REAL Name strings produced by
// internal/docker/checks.go — grepped from production at the time
// of writing. If a future docker package change renames a check,
// this test fails here, forcing the categoriser keywords to be
// updated rather than silently dumping the renamed check into
// "Other".
//
// Specifically catches the two original-cut bugs:
//   - "Docker memory" → must land in Resources (was System because
//     the categoriser checked "docker" before "memory")
//   - "Host prerequisites (linux)" → must land in System (was
//     Other because no keyword matched)
func TestDoctor_CategoriserUpstreamNames(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker CLI", Status: docker.StatusOK},
			{Name: "Docker daemon", Status: docker.StatusOK},
			{Name: "Docker Compose v2", Status: docker.StatusOK},
			{Name: "Docker memory", Status: docker.StatusOK},
			{Name: "Disk space", Status: docker.StatusOK},
			{Name: "Host prerequisites (linux)", Status: docker.StatusOK},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--format=json"})
	_ = cmd.Execute()

	var rep types.PreflightReport
	_ = json.Unmarshal(out.Bytes(), &rep)
	bucket := map[string]string{}
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			bucket[c.Label] = sec.Title
		}
	}
	expect := map[string]string{
		"Docker CLI":                 "System",
		"Docker daemon":              "System",
		"Docker Compose v2":          "System",
		"Docker memory":              "Resources", // <-- the bug fix
		"Disk space":                 "Resources",
		"Host prerequisites (linux)": "System", // <-- the other bug fix
	}
	for label, wantSec := range expect {
		if bucket[label] != wantSec {
			t.Errorf("real upstream check %q in section %q, want %q",
				label, bucket[label], wantSec)
		}
	}
}

// TestDoctor_CategoriserBuckets is the synthetic-fixture sibling.
// Kept because it covers the "Other" fallthrough (no real check
// name does that, but the bucket must exist for forward
// compatibility with future check additions).
func TestDoctor_CategoriserBuckets(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker daemon reachable", Status: docker.StatusOK},
			{Name: "Disk available", Status: docker.StatusOK},
			{Name: "Port 65535 free", Status: docker.StatusOK},
			{Name: "Some new thing", Status: docker.StatusOK},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--format=json"})
	_ = cmd.Execute()

	var rep types.PreflightReport
	_ = json.Unmarshal(out.Bytes(), &rep)

	bucket := map[string]string{}
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			bucket[c.Label] = sec.Title
		}
	}
	cases := map[string]string{
		"Docker daemon reachable": "System",
		"Disk available":          "Resources",
		"Port 65535 free":         "Network",
		"Some new thing":          "Other",
	}
	for label, wantSec := range cases {
		if bucket[label] != wantSec {
			t.Errorf("check %q in section %q, want %q", label, bucket[label], wantSec)
		}
	}
}

// TestResultToken pins the docker.Status → string mapping. JSON
// consumers branch on these tokens — renaming "pass" to "ok" would
// break them silently.
func TestResultToken(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker CLI", Status: docker.StatusOK},
			{Name: "Docker memory", Status: docker.StatusWarn},
			{Name: "Port 65535 free", Status: docker.StatusFail},
			{Name: "Some future check", Status: docker.StatusSkipped},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--format=json"})
	_ = cmd.Execute()

	var rep types.PreflightReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	got := map[string]string{}
	for _, sec := range rep.Sections {
		for _, c := range sec.Checks {
			got[c.Label] = c.Result
		}
	}
	want := map[string]string{
		"Docker CLI":        "pass",
		"Docker memory":     "warn",
		"Port 65535 free":   "fail",
		"Some future check": "skip",
	}
	for label, wantResult := range want {
		if got[label] != wantResult {
			t.Errorf("result for %q = %q, want %q", label, got[label], wantResult)
		}
	}
}

// syntheticTestPort is the port number used in doctor test
// fixtures. 65535 is the maximum TCP port and is deliberately
// outside the range up.go ever allocates from (port allocation
// starts in the low-thousands), so a future change to the real
// port allocator can't accidentally make a test fixture look like
// a real port.
//
// Reviewer pin (PR #39 #3): the original fixtures used 4485 — a
// number close enough to up.go's allocations (4441/4480/4487/etc.)
// that a reader couldn't tell if the test was asserting against
// real behavior or a synthetic placeholder. The clearly-synthetic
// 65535 removes the ambiguity.
const syntheticTestPort = 65535

// TestDoctor_JSONErrorIsSurfacedOnStderr is the reviewer pin
// (PR #39 #5) for the swallowed JSON encode error. The original
// branch returned ExitRuntimeFailure without writing anything to
// stderr, so users saw exit-4 with no diagnostic. We exercise the
// error path by piping --format=json output to a writer that
// fails on Write, and assert the error message reaches stderr.
func TestDoctor_JSONErrorIsSurfacedOnStderr(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker daemon reachable", Status: docker.StatusOK},
		}}
	})

	cmd := buildDoctor()
	var errBuf bytes.Buffer
	cmd.SetOut(&failingWriter{})
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--format=json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when stdout Write fails")
	}
	// The underlying write error MUST appear on stderr — without
	// the fix, stderr would be empty and the user would see only
	// the exit code.
	if errBuf.Len() == 0 {
		t.Errorf("stderr empty — JSON encode error was swallowed (PR #39 #5 regression)")
	}
	if !strings.Contains(errBuf.String(), "write JSON output") {
		t.Errorf("stderr should mention 'write JSON output', got %q", errBuf.String())
	}
}

func TestDoctor_InvalidFormatExitsUserError(t *testing.T) {
	called := false
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		called = true
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker daemon reachable", Status: docker.StatusOK},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--format=xml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	var ece localnet.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if int(ece) != localnet.ExitUserError {
		t.Errorf("exit code = %d, want ExitUserError (%d)", int(ece), localnet.ExitUserError)
	}
	if !strings.Contains(errBuf.String(), "--format must be table or json") {
		t.Errorf("stderr should explain allowed formats, got %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if called {
		t.Error("prober was called for invalid --format; want fail-fast validation")
	}
}

// TestDoctor_TableRendersAsAlignedColumns is the reviewer pin
// (PR #39 #4) for the table-flattening issue. The pre-fix
// renderer emitted a loose stack of Step rows per section; the
// fix uses term.Table so the (glyph, check, detail) columns
// align across rows. We pin the columnar contract by asserting
// that two rows in the same section have their detail strings at
// the same column offset (modulo ANSI). Pre-fix, Step's
// whitespace-padded rendering allowed misalignment.
func TestDoctor_TableRendersAsAlignedColumns(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Docker daemon reachable", Status: docker.StatusOK, Detail: "v25.0.5"},
			{Name: "Compose v2 plugin", Status: docker.StatusOK, Detail: "v2.27.1"},
		}}
	})
	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	_ = cmd.Execute()

	body := out.String()
	// Find the two detail strings — they must appear at the same
	// column offset on their respective lines (proves the table
	// columns line up, which Step-based rendering didn't
	// guarantee).
	stripANSI := func(s string) string {
		var b strings.Builder
		inEsc := false
		for _, r := range s {
			if r == 0x1b {
				inEsc = true
				continue
			}
			if inEsc {
				if r == 'm' {
					inEsc = false
				}
				continue
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	plain := stripANSI(body)
	off := func(line, marker string) int {
		i := strings.Index(line, marker)
		if i < 0 {
			return -1
		}
		return len([]rune(line[:i]))
	}
	var line25, line27 string
	for _, l := range strings.Split(plain, "\n") {
		if strings.Contains(l, "v25.0.5") {
			line25 = l
		}
		if strings.Contains(l, "v2.27.1") {
			line27 = l
		}
	}
	if line25 == "" || line27 == "" {
		t.Fatalf("expected both detail lines in output, got:\n%s", plain)
	}
	if off(line25, "v25.0.5") != off(line27, "v2.27.1") {
		t.Errorf("detail columns misaligned — table flattening regressed:\n  v25 at %d in %q\n  v27 at %d in %q",
			off(line25, "v25.0.5"), line25, off(line27, "v2.27.1"), line27)
	}
}

// failingWriter is an io.Writer that always returns an error.
// Used by TestDoctor_JSONErrorIsSurfacedOnStderr to exercise the
// JSON encoder's error branch.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("simulated stdout failure")
}

// _ = syntheticTestPort silences unused-const lint until a future
// test fixture is updated to reference it directly.
var _ = syntheticTestPort

// TestDoctor_RemediationStepsRenderOnSeparateRows is the reviewer
// pin (PR #39 #4 round-4): the previous renderer joined a multi-
// step remediation into one Step row with " · " separators, so
// three actions ("free port", "retry up", "run doctor") visually
// fused into a single dense line. Each step must now render on
// its own indented numbered row under the check label.
//
// We pin the contract by seeding ONE failing check with THREE
// remediation lines, then asserting the rendered output contains
// three numbered ("1." "2." "3.") rows AND does NOT contain the
// pre-fix " · " separator joining them.
func TestDoctor_RemediationStepsRenderOnSeparateRows(t *testing.T) {
	installFakeDoctorProber(t, func(context.Context, docker.Options) *docker.Report {
		return &docker.Report{Results: []docker.CheckResult{
			{Name: "Port 65535 free", Status: docker.StatusFail, Detail: "pid 88341",
				Remediation: "Stop the conflicting process\nRetry localnet up\nRun localnet doctor again"},
		}}
	})

	cmd := buildDoctor()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	_ = cmd.Execute()

	body := out.String()
	// Three numbered rows must appear.
	for _, want := range []string{"1.", "2.", "3."} {
		if !strings.Contains(body, want) {
			t.Errorf("expected numbered prefix %q in remediation output, got:\n%s", want, body)
		}
	}
	// Each remediation string must appear individually.
	for _, want := range []string{
		"Stop the conflicting process",
		"Retry localnet up",
		"Run localnet doctor again",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("remediation step %q missing from output:\n%s", want, body)
		}
	}
	// The pre-fix " · " separator joining ALL THREE on one line
	// must NOT appear. (A single " · " elsewhere is allowed —
	// term primitives use it for chrome — but the specific
	// "step1 · step2" fusion is the regression marker.)
	bad := "Stop the conflicting process · Retry localnet up"
	if strings.Contains(body, bad) {
		t.Errorf("remediation steps flattened with ' · ' separator (PR #39 #4 regression):\n%s", body)
	}
}
