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
)

// TestThresholdParity_DoctorMatchesUp pins the regression: the
// `doctor && up` shell-gating contract requires BOTH surfaces to
// use the same Min* threshold numbers. An earlier fix introduced
// an 8-GiB doctor vs 4-GiB up drift that broke the gate. The
// DefaultMin*Bytes constants in this package are shared by both
// sites, and this test enforces that nobody re-introduces the
// drift by hand-rolling literals on either site.
//
// Strategy: parse up.go and localnet/doctor.go for the docker.Options
// struct literals they pass to RunPreflight; any MinMemoryBytes / MinDiskBytes
// value that is neither shared DefaultMin*Bytes nor the version-aware splice
// helper used by both surfaces is a regression.
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
		filepath.Join(repoRoot, "internal", "localnet", "doctor.go"),
		// The Web UI preflight handler builds the same PreflightReport
		// and must use the same thresholds, or the CLI gate (`up`/
		// `doctor`) and the Create dialog's pre-flight gate (UI) drift.
		filepath.Join(repoRoot, "internal", "ui", "handlers", "preflight.go"),
	}

	wantedFields := map[string]string{
		"MinMemoryBytes": "DefaultMinMemoryBytes",
		"MinDiskBytes":   "DefaultMinDiskBytes",
	}

	// up.go uses splice.MinMemoryFor(version) /
	// splice.RecommendedMemoryFor(version) instead of the literal
	// docker.DefaultMin*Bytes. Allowed by the parity contract because
	// the splice helpers are invariant-pinned to be >= DefaultMin*Bytes
	// for every catalogued version — they can only RAISE the floor
	// above the doctor gate, never lower it.
	allowedCalls := map[string]struct{}{
		"splice.MinMemoryFor":         {},
		"splice.RecommendedMemoryFor": {},
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
				wantSel, watched := wantedFields[key.Name]
				if !watched {
					return true
				}
				// Allow either docker.DefaultMin* or bare
				// DefaultMin* (in case some future code lives
				// inside the docker package itself).
				if usesAllowedSelector(kv.Value, wantSel) {
					return true
				}
				// also allow a call to one of the
				// splice.{Min,Recommended}MemoryFor helpers.
				if call, ok := kv.Value.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok {
							qname := x.Name + "." + sel.Sel.Name
							if _, ok := allowedCalls[qname]; ok {
								return true
							}
						}
					}
				}
				// Drift detected — produce a useful error with the literal we found.
				literal := exprLiteral(kv.Value)
				t.Errorf("%s:%d: %s = %s — must reference docker.%s or the shared splice helper to keep `doctor && up` in sync",
					filepath.Base(path), fset.Position(kv.Pos()).Line,
					key.Name, literal, wantSel)
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

// usesAllowedSelector returns true if expr is either
// `docker.<name>` or bare `<name>`.
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
