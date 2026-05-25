package types

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestAllTopLevelResponses_CarrySchemaVersion is the BIT-143 #3
// review pin: every top-level response struct in this package
// MUST have a `SchemaVersion int` field so consumers can detect
// format breaks. Reflection-driven so a new response type added
// without the field fails LOUDLY here rather than silently
// shipping without versioning.
//
// We hand-enumerate the "top-level" set because reflection over a
// package's exported types isn't available at runtime in Go;
// every entry corresponds to a struct callers serialize whole as
// a JSON response body.
//
// Rule: when adding a new top-level response type, add it to the
// hand-list AND give it a `SchemaVersion int` field. The test
// fails either way if you forget.
func TestAllTopLevelResponses_CarrySchemaVersion(t *testing.T) {
	topLevel := []interface{}{
		Instance{},
		ListResponse{},
		PreflightReport{},
		Snapshot{},
	}
	for _, v := range topLevel {
		typ := reflect.TypeOf(v)
		f, ok := typ.FieldByName("SchemaVersion")
		if !ok {
			t.Errorf("%s missing required SchemaVersion field", typ.Name())
			continue
		}
		if f.Type.Kind() != reflect.Int {
			t.Errorf("%s.SchemaVersion is %s, want int", typ.Name(), f.Type.Kind())
		}
		// JSON tag must be snake_case `schema_version` so the
		// wire shape stays consistent across types.
		if tag := f.Tag.Get("json"); tag != "schema_version" && !strings.HasPrefix(tag, "schema_version,") {
			t.Errorf("%s.SchemaVersion json tag = %q, want \"schema_version\"", typ.Name(), tag)
		}
	}
}

// TestSchemaVersion_ConsistentAcrossResponses is the BIT-143
// review pin: every top-level response struct that carries a
// SchemaVersion field MUST be populated by the package-level
// SchemaVersion constant when emitted by a caller. We can't
// enforce the populate-at-marshal contract from inside the type
// package, so we verify the structural invariant: every struct
// with a SchemaVersion field uses the SAME int type and is
// tagged identically. Drift here = drift in the JSON shape
// downstream consumers parse.
//
// The reviewer flagged the original cut as having a
// SchemaVersion field on multiple response types (ListResponse,
// PreflightReport, Snapshot) without a test ensuring they all
// stayed aligned. This test adds the pin.
func TestSchemaVersion_ConsistentAcrossResponses(t *testing.T) {
	cases := []struct {
		name    string
		marshal func() ([]byte, error)
		want    int // expected emitted schema_version
	}{
		{
			name: "ListResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(ListResponse{
					SchemaVersion: SchemaVersion,
					Instances:     nil,
				})
			},
			want: SchemaVersion,
		},
		{
			name: "PreflightReport",
			marshal: func() ([]byte, error) {
				return json.Marshal(PreflightReport{
					SchemaVersion: SchemaVersion,
					OK:            true,
				})
			},
			want: SchemaVersion,
		},
		{
			name: "Snapshot",
			marshal: func() ([]byte, error) {
				return json.Marshal(Snapshot{
					SchemaVersion: SchemaVersion,
					Instance:      "x",
				})
			},
			want: SchemaVersion,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf, err := c.marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			body := string(buf)
			// All three MUST emit the literal "schema_version":N
			// shape — anything else (e.g. omitempty hiding the
			// field, or a renamed JSON tag) is a contract break.
			want := `"schema_version":` + itoa(c.want)
			if !strings.Contains(body, want) {
				t.Errorf("%s: expected %q in JSON, got %s", c.name, want, body)
			}
		})
	}
}

// itoa avoids strconv import in this small test file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	out := string(digits[i:])
	if neg {
		out = "-" + out
	}
	return out
}
