package token

import (
	"encoding/json"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// The vetted participants must reach both surfaces, not only the text
// output: a CI consumer of `--format json` or the Web UI has no other
// way to see which participants received the DARs.
func TestCreateResult_ResponseCarriesVettedRoles(t *testing.T) {
	res := &CreateResult{
		TokenRef:    registry.TokenRef{Symbol: "RTK", InstrumentID: "RTK"},
		VettedRoles: []string{"sv", "app-provider", "app-user"},
	}
	raw, err := json.Marshal(res.Response())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SchemaVersion int      `json:"schema_version"`
		Symbol        string   `json:"symbol"`
		InstrumentID  string   `json:"instrument_id"`
		VettedRoles   []string `json:"vetted_roles"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != types.SchemaVersion {
		t.Errorf("schema_version=%d, want %d", got.SchemaVersion, types.SchemaVersion)
	}
	// TokenRef is embedded, so its fields stay at the top level where
	// existing consumers already read them.
	if got.Symbol != "RTK" || got.InstrumentID != "RTK" {
		t.Errorf("TokenRef fields not flattened: %s", raw)
	}
	if len(got.VettedRoles) != 3 {
		t.Errorf("vetted_roles=%v, want 3 roles", got.VettedRoles)
	}
}

// A registry-only create vets nothing, so the key is omitted rather
// than emitted as an empty list that reads like a failed vet.
func TestCreateResult_ResponseOmitsEmptyVettedRoles(t *testing.T) {
	res := &CreateResult{TokenRef: registry.TokenRef{Symbol: "RTK"}}
	raw, err := json.Marshal(res.Response())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, present := got["vetted_roles"]; present {
		t.Errorf("vetted_roles present on a registry-only create: %s", raw)
	}
}
