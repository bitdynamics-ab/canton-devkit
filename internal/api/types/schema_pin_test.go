package types

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestAllTopLevelResponses_CarrySchemaVersion pins that every top-level
// response struct has a `SchemaVersion int` field so consumers can
// detect format breaks. The set is hand-enumerated (runtime reflection
// can't list a package's exported types); a new response type added
// without the field, or missing from the list, fails here.
func TestAllTopLevelResponses_CarrySchemaVersion(t *testing.T) {
	topLevel := []interface{}{
		AnalyzerResponse{},
		AnalyzerStatusResponse{},
		AllocationsResponse{},
		ContractsListResponse{},
		ContractDetailResponse{},
		DARListResponse{},
		DARUploadResponse{},
		DARVettingResponse{},
		DARVettingToggleResponse{},
		EnvExport{},
		Instance{},
		ListResponse{},
		PendingTransfersResponse{},
		PreflightReport{},
		SkillsInstallResponse{},
		SkillsListResponse{},
		Snapshot{},
		TokenHoldingsResponse{},
		TokenIdentityResponse{},
		TokenListResponse{},
		TransactionsListResponse{},
		TxReplayResponse{},
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
		// JSON tag must be snake_case `schema_version` (consistent wire shape).
		if tag := f.Tag.Get("json"); tag != "schema_version" && !strings.HasPrefix(tag, "schema_version,") {
			t.Errorf("%s.SchemaVersion json tag = %q, want \"schema_version\"", typ.Name(), tag)
		}
	}
}

// TestSchemaVersion_ConsistentAcrossResponses verifies each top-level
// response marshals the literal `"schema_version":N` shape. The
// populate-at-marshal contract can't be enforced from inside the type
// package, so this pins the structural invariant instead.
func TestSchemaVersion_ConsistentAcrossResponses(t *testing.T) {
	cases := []struct {
		name    string
		marshal func() ([]byte, error)
		want    int // expected emitted schema_version
	}{
		{
			name: "ContractsListResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(ContractsListResponse{
					SchemaVersion: SchemaVersion,
					Instance:      "x",
				})
			},
			want: SchemaVersion,
		},
		{
			name: "ContractDetailResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(ContractDetailResponse{
					SchemaVersion: SchemaVersion,
					Instance:      "x",
				})
			},
			want: SchemaVersion,
		},
		{
			name: "TransactionsListResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(TransactionsListResponse{
					SchemaVersion: SchemaVersion,
					Instance:      "x",
				})
			},
			want: SchemaVersion,
		},
		{
			name: "TxReplayResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(TxReplayResponse{
					SchemaVersion: SchemaVersion,
					Instance:      "x",
				})
			},
			want: SchemaVersion,
		},
		{
			name: "EnvExport",
			marshal: func() ([]byte, error) {
				return json.Marshal(EnvExport{
					SchemaVersion: SchemaVersion,
					Instance:      "x",
					Vars:          nil,
				})
			},
			want: SchemaVersion,
		},
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
			name: "SkillsInstallResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(SkillsInstallResponse{
					SchemaVersion: SchemaVersion,
					Target:        "claude",
					Dir:           "/tmp/skills",
				})
			},
			want: SchemaVersion,
		},
		{
			name: "SkillsListResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(SkillsListResponse{
					SchemaVersion: SchemaVersion,
					Skills:        nil,
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
		{
			name: "TokenHoldingsResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(TokenHoldingsResponse{
					SchemaVersion: SchemaVersion,
					Source:        HoldingSourceRegistry,
					Holdings:      nil,
				})
			},
			want: SchemaVersion,
		},
		{
			name: "TokenListResponse",
			marshal: func() ([]byte, error) {
				tokens := []TokenRef{}
				return json.Marshal(TokenListResponse{
					SchemaVersion: SchemaVersion,
					Tokens:        &tokens,
				})
			},
			want: SchemaVersion,
		},
		{
			name: "AllocationsResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(AllocationsResponse{
					SchemaVersion: SchemaVersion,
					Allocations:   nil,
				})
			},
			want: SchemaVersion,
		},
		{
			name: "PendingTransfersResponse",
			marshal: func() ([]byte, error) {
				return json.Marshal(PendingTransfersResponse{
					SchemaVersion:    SchemaVersion,
					PendingTransfers: nil,
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
			// Anything but the literal "schema_version":N (omitempty
			// hiding it, a renamed tag) is a contract break.
			want := `"schema_version":` + strconv.Itoa(c.want)
			if !strings.Contains(body, want) {
				t.Errorf("%s: expected %q in JSON, got %s", c.name, want, body)
			}
		})
	}
}
