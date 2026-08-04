package token

import (
	"context"
	"encoding/json"
	"testing"
)

func TestListInstruments_LiveEmptyPreservesLiveBranch(t *testing.T) {
	withFakeDial(t, &fakeLedger{})
	resp, err := ListInstruments(context.Background(), BalanceOptions{
		Endpoint: "localhost:7501",
		Role:     "app-user",
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	if resp.Instruments == nil || len(*resp.Instruments) != 0 {
		t.Fatalf("live empty result = %+v, want a selected empty instruments slice", resp.Instruments)
	}
	if resp.Tokens != nil {
		t.Fatalf("live result must not select recorded tokens: %+v", resp.Tokens)
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(decoded["instruments"]); got != "[]" {
		t.Errorf("live empty wire result = %q, want instruments:[] in %s", got, body)
	}
}
