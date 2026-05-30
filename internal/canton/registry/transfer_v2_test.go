package registry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestGetTransferFactory_HappyPath asserts the request path, body shape
// the registry sees, bearer auth, and that the typed response decode
// preserves every field the on-ledger submit step downstream needs:
// factoryId, transferKind, choiceContextData, disclosedContracts.
func TestGetTransferFactory_HappyPath(t *testing.T) {
	var capturedPath, capturedAuth, capturedCT string
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		capturedCT = r.Header.Get("Content-Type")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		// Per the OpenAPI TransferFactoryWithChoiceContext schema,
		// choiceContextData + disclosedContracts are nested under
		// `choiceContext`, NOT at the top level.
		_, _ = w.Write([]byte(`{
			"factoryId": "00abc-factory",
			"transferKind": "Offer",
			"choiceContext": {
				"choiceContextData": {"k": "v"},
				"disclosedContracts": [
					{"contractId": "00cid-1", "createdEventBlob": "Zm9v", "synchronizerId": "global-domain"}
				]
			}
		}`))
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL, Token: StaticToken("test-token")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	req := TransferFactoryRequest{
		ChoiceArguments: TransferFactoryChoiceArgs{
			Actors: []string{"alice::1220"},
			Transfer: TransferArgs{
				Sender:           NewOwnedAccount("alice::1220"),
				Receiver:         NewOwnedAccount("bob::1220"),
				Amount:           "10.00",
				InstrumentID:     InstrumentID{Admin: "DSO::1220", ID: "Amulet"},
				RequestedAt:      time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
				ExecuteBefore:    time.Date(2026, 5, 30, 13, 0, 0, 0, time.UTC),
				InputHoldingCids: []string{"00cid-h1"},
				Meta:             Metadata{Values: map[string]string{}},
			},
			ExtraArgs: ExtraArgs{
				Context: Metadata{Values: map[string]string{}},
				Meta:    Metadata{Values: map[string]string{}},
			},
		},
	}

	resp, err := c.GetTransferFactory(context.Background(), req)
	if err != nil {
		t.Fatalf("GetTransferFactory: %v", err)
	}
	// Wire-level assertions.
	if capturedPath != transferFactoryPath {
		t.Errorf("path: got %q, want %q", capturedPath, transferFactoryPath)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("auth: got %q, want %q", capturedAuth, "Bearer test-token")
	}
	if capturedCT != "application/json" {
		t.Errorf("content-type: got %q, want application/json", capturedCT)
	}
	// Body must carry the nested choiceArguments structure exactly as
	// the OpenAPI spec requires — the registry parses by JSON path.
	var decoded TransferFactoryRequest
	if err := json.Unmarshal(capturedBody, &decoded); err != nil {
		t.Fatalf("body unmarshal: %v\nbody=%s", err, capturedBody)
	}
	if decoded.ChoiceArguments.Transfer.Sender.Owner == nil || *decoded.ChoiceArguments.Transfer.Sender.Owner != "alice::1220" {
		t.Errorf("sender.owner: got %+v", decoded.ChoiceArguments.Transfer.Sender)
	}
	if len(decoded.ChoiceArguments.Actors) != 1 || decoded.ChoiceArguments.Actors[0] != "alice::1220" {
		t.Errorf("actors: got %v", decoded.ChoiceArguments.Actors)
	}
	if decoded.ChoiceArguments.Transfer.InstrumentID.ID != "Amulet" {
		t.Errorf("instrumentId.id: got %q", decoded.ChoiceArguments.Transfer.InstrumentID.ID)
	}
	if len(decoded.ChoiceArguments.Transfer.InputHoldingCids) != 1 {
		t.Errorf("inputHoldingCids: got %d entries, want 1", len(decoded.ChoiceArguments.Transfer.InputHoldingCids))
	}
	// Response decode assertions.
	if resp.FactoryID != "00abc-factory" {
		t.Errorf("factoryId: got %q", resp.FactoryID)
	}
	if resp.TransferKind != TransferKindOffer {
		t.Errorf("transferKind: got %q, want %q", resp.TransferKind, TransferKindOffer)
	}
	disclosed := resp.DisclosedContractsList()
	if len(disclosed) != 1 {
		t.Fatalf("disclosedContracts: got %d, want 1", len(disclosed))
	}
	if disclosed[0].ContractID != "00cid-1" {
		t.Errorf("disclosed[0].contractId: got %q", disclosed[0].ContractID)
	}
	if disclosed[0].CreatedEventBlob != "Zm9v" {
		t.Errorf("disclosed[0].createdEventBlob: got %q", disclosed[0].CreatedEventBlob)
	}
	if v, _ := resp.ChoiceContextData()["k"].(string); v != "v" {
		t.Errorf("choiceContextData[k]: got %q, want v", v)
	}
}

// TestGetAcceptChoiceContext_HappyPath asserts the per-instruction path
// substitution and minimal request body shape.
func TestGetAcceptChoiceContext_HappyPath(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choiceContextData": {"accepted": true},
			"disclosedContracts": []
		}`))
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL, Token: StaticToken("x")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	resp, err := c.GetAcceptChoiceContext(
		context.Background(),
		"00txn-instruction-1",
		ChoiceContextRequest{Meta: Metadata{Values: map[string]string{}}},
	)
	if err != nil {
		t.Fatalf("GetAcceptChoiceContext: %v", err)
	}
	wantPath := "/registry/transfer-instruction/v2/00txn-instruction-1/choice-contexts/accept"
	if capturedPath != wantPath {
		t.Errorf("path: got %q, want %q", capturedPath, wantPath)
	}
	if v, _ := resp.ChoiceContextData["accepted"].(bool); !v {
		t.Errorf("choiceContextData.accepted: got %v, want true", resp.ChoiceContextData["accepted"])
	}
}

// TestGetAcceptChoiceContext_EmptyIDRejected pins the input-validation
// fast-fail. The registry would also return 404, but failing client-
// side avoids an unauthenticated request hitting the network with an
// obviously-invalid path.
func TestGetAcceptChoiceContext_EmptyIDRejected(t *testing.T) {
	c, err := Dial(DialOptions{BaseURL: "http://localhost"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_, err = c.GetAcceptChoiceContext(context.Background(), "", ChoiceContextRequest{})
	if err == nil || !strings.Contains(err.Error(), "instructionID is required") {
		t.Errorf("want instructionID required error, got %v", err)
	}
}

// TestGetTransferFactory_PropagatesAPIError asserts that a 4xx from the
// registry (e.g. fee schedule violation) round-trips via APIError so
// the caller can surface the actual reason to the user.
func TestGetTransferFactory_PropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": "insufficient funds"}`))
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_, err = c.GetTransferFactory(context.Background(), TransferFactoryRequest{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var apiErr *APIError
	if !errorsAs(err, &apiErr) {
		t.Fatalf("want APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", apiErr.StatusCode)
	}
	if !strings.Contains(string(apiErr.Body), "insufficient funds") {
		t.Errorf("body missing reason: %s", apiErr.Body)
	}
}

// errorsAs is a small helper so this test file doesn't have to import
// "errors" just for one call site — the file already deals with HTTP
// + JSON and we want to keep the import set tight.
func errorsAs(err error, target **APIError) bool {
	for e := err; e != nil; {
		if a, ok := e.(*APIError); ok {
			*target = a
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
