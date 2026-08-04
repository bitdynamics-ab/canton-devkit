package types

import (
	"encoding/json"
	"testing"
)

func TestTokenListResponse_PreservesSelectedEmptyArray(t *testing.T) {
	cases := []struct {
		name     string
		response TokenListResponse
		present  string
		absent   string
	}{
		{
			name: "live instruments",
			response: func() TokenListResponse {
				items := []InstrumentRef{}
				return TokenListResponse{SchemaVersion: SchemaVersion, Instruments: &items}
			}(),
			present: "instruments",
			absent:  "tokens",
		},
		{
			name: "recorded tokens",
			response: func() TokenListResponse {
				items := []TokenRef{}
				return TokenListResponse{SchemaVersion: SchemaVersion, Tokens: &items}
			}(),
			present: "tokens",
			absent:  "instruments",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.response)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := string(decoded[tc.present]); got != "[]" {
				t.Errorf("%s = %q, want [] in %s", tc.present, got, body)
			}
			if _, ok := decoded[tc.absent]; ok {
				t.Errorf("%s must be omitted in %s", tc.absent, body)
			}
		})
	}
}
