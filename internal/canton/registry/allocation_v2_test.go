package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestGetAllocationFactory_HappyPath asserts the request path, body shape,
// bearer auth, and that the typed response decode preserves the fields the
// on-ledger AllocationFactory_Allocate submit needs: factoryId,
// choiceContextData, disclosedContracts.
func TestGetAllocationFactory_HappyPath(t *testing.T) {
	var capturedPath, capturedAuth string
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"factoryId": "00alloc-factory",
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

	deadline := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	req := AllocationFactoryRequest{
		ChoiceArguments: AllocationFactoryChoiceArgs{
			ExpectedAdmin: "DSO::1220",
			Settlement: SettlementInfo{
				Executor:      "alice::1220",
				SettlementRef: Reference{ID: "settlement-1"},
				Meta:          Metadata{Values: map[string]string{}},
			},
			Allocation: AllocationSpecification{
				Admin:      "DSO::1220",
				Authorizer: NewOwnedAccount("alice::1220"),
				TransferLegSides: []TransferLegSide{{
					TransferLegID: "leg0",
					TransferLeg: TransferLeg{
						Sender:       NewOwnedAccount("alice::1220"),
						Receiver:     NewOwnedAccount("bob::1220"),
						Amount:       "10.0",
						InstrumentID: InstrumentID{Admin: "DSO::1220", ID: "RTK"},
						Meta:         Metadata{Values: map[string]string{}},
					},
				}},
				SettlementDeadline: &deadline,
				Committed:          true,
				Meta:               Metadata{Values: map[string]string{}},
			},
			InputHoldingCids: []string{"00cid-h1"},
			Actors:           []string{"alice::1220"},
			ExtraArgs: ExtraArgs{
				Context: Metadata{Values: map[string]string{}},
				Meta:    Metadata{Values: map[string]string{}},
			},
		},
	}

	resp, err := c.GetAllocationFactory(context.Background(), "/registry/allocation-instruction/v2/allocation-factory", req)
	if err != nil {
		t.Fatalf("GetAllocationFactory: %v", err)
	}
	if capturedPath != "/registry/allocation-instruction/v2/allocation-factory" {
		t.Errorf("path: got %q", capturedPath)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("auth: got %q", capturedAuth)
	}
	// Body must round-trip the nested choiceArguments the registry parses.
	var decoded AllocationFactoryRequest
	if err := json.Unmarshal(capturedBody, &decoded); err != nil {
		t.Fatalf("body unmarshal: %v\nbody=%s", err, capturedBody)
	}
	if decoded.ChoiceArguments.ExpectedAdmin != "DSO::1220" {
		t.Errorf("expectedAdmin: got %q", decoded.ChoiceArguments.ExpectedAdmin)
	}
	if !decoded.ChoiceArguments.Allocation.Committed {
		t.Errorf("committed did not round-trip: %s", capturedBody)
	}
	if decoded.ChoiceArguments.Allocation.SettlementDeadline == nil {
		t.Errorf("settlementDeadline did not round-trip: %s", capturedBody)
	}
	if n := len(decoded.ChoiceArguments.Allocation.TransferLegSides); n != 1 {
		t.Errorf("transferLegSides: got %d, want 1", n)
	}
	// Response decode assertions.
	if resp.FactoryID != "00alloc-factory" {
		t.Errorf("factoryId: got %q", resp.FactoryID)
	}
	if d := resp.DisclosedContractsList(); len(d) != 1 || d[0].ContractID != "00cid-1" {
		t.Errorf("disclosedContracts: got %+v", d)
	}
	if v, _ := resp.ChoiceContextData()["k"].(string); v != "v" {
		t.Errorf("choiceContextData[k]: got %q", v)
	}
}

// TestGetAllocationFactory_NonCommittedOmitsDeadline pins that a
// non-committed allocation with no deadline serialises settlementDeadline
// as null (Optional None), which the registry decodes as no deadline.
func TestGetAllocationFactory_NonCommittedOmitsDeadline(t *testing.T) {
	spec := AllocationSpecification{
		Admin:      "DSO::1220",
		Authorizer: NewOwnedAccount("alice::1220"),
		Committed:  false,
		Meta:       Metadata{Values: map[string]string{}},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := got["settlementDeadline"]; !ok || v != nil {
		t.Errorf("settlementDeadline: want null, got %v (present=%v)", v, ok)
	}
	if got["committed"] != false {
		t.Errorf("committed: got %v, want false", got["committed"])
	}
}

// TestGetSettlementFactory_HappyPath asserts the GET path + response decode.
func TestGetSettlementFactory_HappyPath(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"factoryId": "00settle-factory",
			"choiceContext": {"choiceContextData": {}, "disclosedContracts": []}
		}`))
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL, Token: StaticToken("x")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	resp, err := c.GetSettlementFactory(context.Background(), "/registry/allocation/v2/settlement-factory")
	if err != nil {
		t.Fatalf("GetSettlementFactory: %v", err)
	}
	if capturedMethod != http.MethodGet {
		t.Errorf("method: got %q, want GET", capturedMethod)
	}
	if capturedPath != "/registry/allocation/v2/settlement-factory" {
		t.Errorf("path: got %q", capturedPath)
	}
	if resp.FactoryID != "00settle-factory" {
		t.Errorf("factoryId: got %q", resp.FactoryID)
	}
}

// TestGetAllocationChoiceContext_SubstitutesID pins that the {allocationId}
// placeholder is replaced with the url-escaped id, and the response decodes.
func TestGetAllocationChoiceContext_SubstitutesID(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choiceContextData":{"ok":true},"disclosedContracts":[]}`))
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL, Token: StaticToken("x")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	resp, err := c.GetAllocationChoiceContext(context.Background(),
		"/registry/allocations/v2/{allocationId}/choice-contexts/withdraw",
		"00alloc-1",
		ChoiceContextRequest{Meta: map[string]string{}})
	if err != nil {
		t.Fatalf("GetAllocationChoiceContext: %v", err)
	}
	want := "/registry/allocations/v2/00alloc-1/choice-contexts/withdraw"
	if capturedPath != want {
		t.Errorf("path: got %q, want %q", capturedPath, want)
	}
	if v, _ := resp.ChoiceContextData["ok"].(bool); !v {
		t.Errorf("choiceContextData.ok: got %v", resp.ChoiceContextData["ok"])
	}
}

// TestGetAllocationChoiceContext_EmptyIDRejected pins the client-side
// fast-fail before an obviously-invalid request hits the network.
func TestGetAllocationChoiceContext_EmptyIDRejected(t *testing.T) {
	c, err := Dial(DialOptions{BaseURL: "http://localhost"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_, err = c.GetAllocationChoiceContext(context.Background(),
		"/registry/allocations/v2/{allocationId}/choice-contexts/cancel", "",
		ChoiceContextRequest{})
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("want id-required error, got %v", err)
	}
}

// TestGetAllocationChoiceContext_EscapesReservedChars pins that an id
// carrying URL-reserved chars ('#', '?') is escaped so the registry sees
// the literal id (no truncation at the '#').
func TestGetAllocationChoiceContext_EscapesReservedChars(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choiceContextData":{},"disclosedContracts":[]}`))
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL, Token: StaticToken("x")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := c.GetAllocationChoiceContext(context.Background(),
		"/registry/allocations/v2/{allocationId}/choice-contexts/cancel",
		"00alloc id#hash?q", ChoiceContextRequest{Meta: map[string]string{}}); err != nil {
		t.Fatalf("GetAllocationChoiceContext: %v", err)
	}
	if !strings.Contains(capturedPath, "00alloc id#hash?q") {
		t.Errorf("path lost reserved chars: %q", capturedPath)
	}
}

// TestGetAllocationFactory_PropagatesAPIError asserts a 4xx round-trips via
// APIError so the caller can surface the registry's actual reason.
func TestGetAllocationFactory_PropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "INSUFFICIENT_FUNDS"}`))
	}))
	defer srv.Close()

	c, err := Dial(DialOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_, err = c.GetAllocationFactory(context.Background(),
		"/registry/allocation-instruction/v2/allocation-factory",
		AllocationFactoryRequest{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("want APIError 400, got %v", err)
	}
	if !strings.Contains(apiErr.Body, "INSUFFICIENT_FUNDS") {
		t.Errorf("body missing reason: %s", apiErr.Body)
	}
}
