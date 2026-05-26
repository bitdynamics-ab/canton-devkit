package docker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// TestThresholdParity_DoctorMatchesUp pins the PR #39 follow-up
// regression: the `doctor && up` shell-gating contract requires
// BOTH surfaces to enforce thresholds that are equal OR where up
// is strictly stricter than doctor. The original PR #39 fix
// introduced an 8-GiB doctor vs 4-GiB up drift in the wrong
// direction (doctor would refuse a host that up would happily
// run on), which broke `doctor && up`.
//
// Strategy: parse both up.go and doctor.go for the docker.Options
// struct literals they pass to RunPreflight; check each watched
// field uses an allowed expression shape.
//
// Allowed shapes:
//
//   - Bare or qualified DefaultMin*Bytes identifier (doctor.go uses
//     this — it's version-agnostic so it pins to the global floor).
//   - A function call expression like splice.MinMemoryFor(version)
//     (up.go uses this — it knows the version being brought up and
//     enforces a per-version floor >= the global default; the
//     ≥-default invariant is enforced separately by
//     TestThresholdParity_VersionMinAtLeastDefault).
//   - DefaultMinDiskBytes — disk is not version-scoped today, both
//     surfaces share the constant.
func TestThresholdParity_DoctorMatchesUp(t *testing.T) {
	// Locate the two source files relative to this test (works
	// regardless of cwd).
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	files := []string{
		filepath.Join(repoRoot, "internal", "localnet", "up.go"),
		filepath.Join(repoRoot, "internal", "cli", "localnet", "doctor.go"),
	}

	// Per-field allowed expressions. MinDiskBytes still requires the
	// constant identifier on both surfaces (not version-scoped).
	// MinMemoryBytes additionally allows splice.MinMemoryFor() —
	// the version-aware accessor used by up.go after BIT-178.
	// `rule` is a package-level type alias declared below so the
	// helper exprMatchesRule shares the same shape.
	wantedFields := map[string]rule{
		"MinMemoryBytes":         {constName: "DefaultMinMemoryBytes", allowedCalls: []string{"MinMemoryFor"}},
		"MinDiskBytes":           {constName: "DefaultMinDiskBytes"},
		"RecommendedMemoryBytes": {constName: "", allowedCalls: []string{"RecommendedMemoryFor"}},
	}

	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				// Per-ticket PR flow: some target files live on
				// a different branch and won't be on the current
				// HEAD until merge. Skip rather than fail so the
				// test stays portable across branches; once both
				// land on main, both files exist and the parity
				// check fires.
				if os.IsNotExist(err) {
					t.Skipf("%s not present on this branch (per-ticket flow) — skipping; parity enforced when both files exist", path)
				}
				t.Fatalf("read %s: %v", path, err)
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, body, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				kv, ok := n.(*ast.KeyValueExpr)
				if !ok {
					return true
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					return true
				}
				r, watched := wantedFields[key.Name]
				if !watched {
					return true
				}
				if exprMatchesRule(kv.Value, r) {
					return true
				}
				// Drift detected — produce a useful error with
				// the literal we found.
				literal := exprLiteral(kv.Value)
				want := r.constName
				if len(r.allowedCalls) > 0 {
					want += " (or " + strings.Join(r.allowedCalls, "/") + " call)"
				}
				t.Errorf("%s:%d: %s = %s — must be %s to keep `doctor && up` in sync",
					filepath.Base(path), fset.Position(kv.Pos()).Line,
					key.Name, literal, want)
				return true
			})
		})
	}

	// Cross-check the constants are positive and within sane
	// dev-machine bounds — a 0 would silently disable the check
	// in both surfaces.
	if DefaultMinDiskBytes == 0 || DefaultMinMemoryBytes == 0 {
		t.Errorf("DefaultMin* must be >0; got disk=%d memory=%d",
			DefaultMinDiskBytes, DefaultMinMemoryBytes)
	}
	if DefaultMinMemoryBytes > 64*1024*1024*1024 {
		t.Errorf("DefaultMinMemoryBytes = %d looks too large (suspicious typo?)",
			DefaultMinMemoryBytes)
	}
}

// TestThresholdParity_VersionMinAtLeastDefault asserts the floor
// invariant: for every catalogued Splice version, the effective
// memory minimum (resolved via splice.MinMemoryFor) must be >=
// the global docker.DefaultMinMemoryBytes. This is what keeps the
// `doctor && up` contract intact even though up.go now passes a
// per-version value instead of the bare constant — a version-
// specific override can only RAISE the gate, never weaken it.
//
// If a future maintainer ever sets a catalogued
// MinMemoryBytes below DefaultMinMemoryBytes (e.g. "this version
// is lighter, let's allow 2 GiB"), this test fires and the
// regression is caught before merge.
func TestThresholdParity_VersionMinAtLeastDefault(t *testing.T) {
	for _, tag := range splice.Supported() {
		v := splice.SupportedVersions[tag]
		got := splice.MinMemoryFor(v)
		if got < DefaultMinMemoryBytes {
			t.Errorf("splice %s: MinMemoryFor = %d (< DefaultMinMemoryBytes %d) — a version override may only TIGHTEN the gate",
				tag, got, DefaultMinMemoryBytes)
		}
		if v.RecommendedMemoryBytes > 0 && v.RecommendedMemoryBytes < got {
			t.Errorf("splice %s: RecommendedMemoryBytes %d < MinMemoryBytes %d — recommended must be at least the minimum",
				tag, v.RecommendedMemoryBytes, got)
		}
	}
}

// exprMatchesRule returns true if expr is the rule's bare/qualified
// constant identifier OR a call expression to one of the allowed
// selectors (e.g. splice.MinMemoryFor).
func exprMatchesRule(expr ast.Expr, r rule) bool {
	// Identifier or qualified selector match.
	if r.constName != "" {
		switch e := expr.(type) {
		case *ast.SelectorExpr:
			if e.Sel.Name == r.constName {
				return true
			}
		case *ast.Ident:
			if e.Name == r.constName {
				return true
			}
		}
	}
	// Call expression match — e.g. splice.MinMemoryFor(version).
	if call, ok := expr.(*ast.CallExpr); ok && len(r.allowedCalls) > 0 {
		var name string
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		case *ast.Ident:
			name = fn.Name
		}
		for _, allowed := range r.allowedCalls {
			if name == allowed {
				return true
			}
		}
	}
	return false
}

// rule is declared at package scope so exprMatchesRule can refer to
// it; named here for clarity (the test body uses an anonymous
// struct alias).
type rule = struct {
	constName    string
	allowedCalls []string
}

// usesAllowedSelector — kept for compatibility with any future test
// that wants the simpler identifier-only check.
func usesAllowedSelector(expr ast.Expr, name string) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == name
	case *ast.Ident:
		return e.Name == name
	}
	return false
}

// exprLiteral renders a small ast.Expr back as text for the
// error message. We handle the only two shapes we actually
// expect (BasicLit and BinaryExpr like "4 * 1024 * 1024 * 1024")
// because go/printer would add a transitive dep we don't want.
func exprLiteral(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Value
	case *ast.BinaryExpr:
		return exprLiteral(e.X) + " " + e.Op.String() + " " + exprLiteral(e.Y)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
		return e.Sel.Name
	case *ast.CallExpr:
		return exprLiteral(e.Fun) + "(...)"
	}
	return "<expr>"
}

// uintCheck is a tiny helper so the test file can avoid an extra
// strconv import for the error message; kept here for future
// extension of the regression test set.
func uintCheck(s string) (uint64, error) { return strconv.ParseUint(strings.TrimSpace(s), 10, 64) }

// _ keeps uintCheck reachable for future tests without lint
// complaining about it being unused.
var _ = uintCheck

// _ keeps usesAllowedSelector reachable for future tests; the
// rewrite to exprMatchesRule (BIT-178) made the original helper
// unused but the simpler shape is still useful for future single-
// rule parity checks.
var _ = usesAllowedSelector
