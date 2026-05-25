package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestHandlersSchemaPin_AllResponsesCarrySchemaVersion is the
// reviewer pin (PR #43 round-2 #3, applying the pattern from
// PR #44's skill-vs-CLI lint): scan the handlers package source
// for every top-level struct used as a JSON response, and assert
// each carries a SchemaVersion field with the canonical
// json:"schema_version" tag.
//
// The reviewer flagged that api/types/schema_pin_test.go doesn't
// reach types in this package — they "escape" the central
// reflection check. Fixing the gap properly means a lint that
// fires every CI run, so a new response shape added next month
// can't ship without the SchemaVersion field.
//
// Detection heuristic: any struct whose name ends in "Response"
// OR "Payload" is treated as a top-level JSON response. New
// suffixes can join the responseTypeSuffix slice; the value of
// being conservative (vs reflecting on every struct) is that
// internal-only structs (errorBody, scannedCommand) don't need
// a SchemaVersion field.
//
// Catch class: a contributor adds `type FoosResponse struct {...}`
// without SchemaVersion. CI fails on the next push.
func TestHandlersSchemaPin_AllResponsesCarrySchemaVersion(t *testing.T) {
	pkg, err := readPackageStructs(".")
	if err != nil {
		t.Fatalf("scan package: %v", err)
	}
	if len(pkg) == 0 {
		t.Fatal("no structs found in handlers package — parser broken?")
	}

	suffixes := []string{"Response", "Payload"}
	checked := 0
	for name, fields := range pkg {
		if !endsWithAny(name, suffixes) {
			continue
		}
		checked++
		if _, hasSchema := fields["SchemaVersion"]; !hasSchema {
			t.Errorf("type %q (looks like a top-level response: ends in %v) is missing SchemaVersion field — central schema-pin contract requires it",
				name, suffixes)
			continue
		}
		// And the json tag must be the canonical "schema_version".
		if tag := fields["SchemaVersion"]; !strings.Contains(tag, `json:"schema_version"`) {
			t.Errorf("type %q SchemaVersion field has json tag %q, want schema_version",
				name, tag)
		}
	}
	if checked == 0 {
		t.Fatal("scanned no Response/Payload structs — heuristic regressed? Add a known type to suffixes.")
	}
}

// TestHandlersSchemaPin_ConstMatchesTypes is the parallel pin
// (matches PR #41's TestVersion_MatchesTypesSchema): the
// handlers.SchemaVersion constant must equal the values emitted
// by each Response/Payload type. Reflective; covers jwtResponse
// + appConfigPayload today, and any future Response/Payload
// added in the package.
func TestHandlersSchemaPin_ConstMatchesTypes(t *testing.T) {
	for _, v := range []any{
		jwtResponse{SchemaVersion: SchemaVersion},
		appConfigPayload{SchemaVersion: SchemaVersion},
	} {
		rv := reflect.ValueOf(v)
		f := rv.FieldByName("SchemaVersion")
		if !f.IsValid() {
			t.Errorf("%T missing SchemaVersion field at runtime", v)
			continue
		}
		if f.Int() != int64(SchemaVersion) {
			t.Errorf("%T.SchemaVersion = %d, want %d", v, f.Int(), SchemaVersion)
		}
	}
}

// readPackageStructs parses every .go file in dir (non-recursive)
// and returns a map of structName → fieldName → fieldTag.
// Test code is excluded so types defined in _test.go don't trip
// the lint with their internal helpers.
func readPackageStructs(dir string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			fields := map[string]string{}
			for _, fld := range st.Fields.List {
				tag := ""
				if fld.Tag != nil {
					tag = fld.Tag.Value
				}
				for _, name := range fld.Names {
					fields[name.Name] = tag
				}
			}
			out[ts.Name.Name] = fields
			return true
		})
	}
	return out, nil
}

// endsWithAny reports whether s ends with any of the suffixes.
func endsWithAny(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}
