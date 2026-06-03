package token

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestTokenRefJSONParity pins that registry.TokenRef (persisted in
// state.json) and types.TokenRef (the HTTP wire shape) keep an identical
// JSON shape. They're deliberately duplicated across the import boundary
// — registry mustn't depend on api/types and vice versa — so nothing but
// a test catches a field or tag drifting on one side and silently
// breaking CLI ↔ HTTP round-tripping.
func TestTokenRefJSONParity(t *testing.T) {
	src := registry.TokenRef{
		Name:          "Retail Token",
		Symbol:        "RTK",
		Decimals:      6,
		InitialSupply: "1000000",
		IssuerParty:   "alice::1220ab",
		InstrumentID:  "instr-1",
		CreatedAt:     "2026-06-03T00:00:00Z",
		Status:        "active",
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}

	// A value from one side must decode into the other and re-encode
	// byte-identically.
	var dst types.TokenRef
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("registry.TokenRef JSON does not fit types.TokenRef: %v", err)
	}
	rawBack, err := json.Marshal(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(rawBack) {
		t.Errorf("JSON shape drift between the two TokenRef structs:\n registry: %s\n types:    %s", raw, rawBack)
	}

	// Structural tag-set equality also catches an ADDED zero-valued field
	// (which would round-trip fine above but still be a drift).
	if got, want := jsonTags(reflect.TypeOf(types.TokenRef{})), jsonTags(reflect.TypeOf(registry.TokenRef{})); !reflect.DeepEqual(got, want) {
		t.Errorf("json tag sets differ:\n types:    %v\n registry: %v", got, want)
	}
}

func jsonTags(t reflect.Type) []string {
	tags := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tags = append(tags, t.Field(i).Tag.Get("json"))
	}
	sort.Strings(tags)
	return tags
}
