package telemetry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAllCallSitesAreAllowListed walks every telemetry.Inc(...) call site
// in the repo (design decision #17.1) and asserts: the chart literal is a
// known chart, and when the bucket is also a literal, (chart,bucket) is
// allow-listed. This catches a new counter or a typo'd bucket slipped
// past review — the allow-list is the single source of truth.
func TestAllCallSitesAreAllowListed(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	var violations []string
	_ = filepathWalkGo(root, func(path string) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isIncCall(call) || len(call.Args) != 2 {
				return true
			}
			chart, chartLit := stringLit(call.Args[0])
			if !chartLit {
				return true // dynamic chart — can't statically check
			}
			if _, known := allowedCounters[chart]; !known {
				violations = append(violations, path+": unknown chart "+strconv.Quote(chart))
				return true
			}
			if bucket, bucketLit := stringLit(call.Args[1]); bucketLit {
				if !allowed(chart, bucket) {
					violations = append(violations, path+": "+chart+" bucket "+strconv.Quote(bucket)+" not allow-listed")
				}
			}
			return true
		})
	})
	if len(violations) > 0 {
		t.Fatalf("telemetry.Inc call sites violate the allow-list:\n  %s", strings.Join(violations, "\n  "))
	}
}

func isIncCall(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr: // telemetry.Inc(...)
		return fn.Sel.Name == "Inc"
	case *ast.Ident: // Inc(...) inside the telemetry package
		return fn.Name == "Inc"
	}
	return false
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func repoRoot(t *testing.T) string {
	// test runs in internal/telemetry → repo root is two levels up.
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func filepathWalkGo(root string, fn func(path string)) error {
	return walk(root, func(p string) {
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			fn(p)
		}
	})
}
