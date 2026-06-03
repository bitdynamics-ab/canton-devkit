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
)

// dialAgainst spins up an httptest.Server with `h` and returns a Client
// pointing at it. Standardises the test boilerplate so every case below
// stays under 10 lines of setup.
func dialAgainst(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := Dial(DialOptions{BaseURL: srv.URL, Token: StaticToken("unsafe-test-token")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return c
}

// TestGetTransferFactory_HappyPath: the request body is forwarded as
// JSON, the Bearer token reaches the server, and the typed response
// round-trips ChoiceContext + DisclosedContract verbatim.
func TestGetTransferFactory_HappyPath(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody map[string]any
	c := dialAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{
		  "factoryId": "factory-cid-001",
		  "transferKind": "offer",
		  "choiceContext": {
		    "choiceContextData": {"someKey": "someValue"},
		    "disclosedContracts": [
		      {"templateId":"Splice.Amulet:Amulet","contractId":"cid-1","createdEventBlob":"BLOB1","synchronizerId":"global::DSO"}
		    ]
		  }
		}`))
	}))

	out, err := c.GetTransferFactory(context.Background(),
		map[string]any{
			"expectedAdmin": "issuer::abc",
			"transfer": map[string]any{
				"sender": "alice", "receiver": "bob", "amount": "10",
			},
		}, true /* excludeDebug */)
	if err != nil {
		t.Fatalf("GetTransferFactory: %v", err)
	}

	if gotMethod != "POST" || gotPath != pathTransferFactory {
		t.Errorf("request line = %s %s; want POST %s", gotMethod, gotPath, pathTransferFactory)
	}
	if gotAuth != "Bearer unsafe-test-token" {
		t.Errorf("Authorization = %q; want Bearer token forwarded", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", gotContentType)
	}
	if gotBody["excludeDebugFields"] != true {
		t.Errorf("excludeDebugFields not forwarded; body = %+v", gotBody)
	}
	if got := gotBody["choiceArguments"].(map[string]any); got["expectedAdmin"] != "issuer::abc" {
		t.Errorf("choiceArguments not forwarded verbatim; got %+v", got)
	}

	if out.FactoryID != "factory-cid-001" {
		t.Errorf("FactoryID = %q, want factory-cid-001", out.FactoryID)
	}
	if out.TransferKind != TransferKindOffer {
		t.Errorf("TransferKind = %q, want offer", out.TransferKind)
	}
	if len(out.ChoiceContext.DisclosedContracts) != 1 {
		t.Fatalf("DisclosedContracts len = %d, want 1", len(out.ChoiceContext.DisclosedContracts))
	}
	dc := out.ChoiceContext.DisclosedContracts[0]
	if dc.ContractID != "cid-1" || dc.CreatedEventBlob != "BLOB1" || dc.SynchronizerID != "global::DSO" {
		t.Errorf("DisclosedContract round-trip drifted: %+v", dc)
	}
}

// TestGetTransferFactory_APIErrorOn4xx pins the contract that non-2xx
// responses become a typed *APIError the caller can `errors.As`.
func TestGetTransferFactory_APIErrorOn4xx(t *testing.T) {
	c := dialAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"holding contended"}`))
	}))

	_, err := c.GetTransferFactory(context.Background(), map[string]any{}, false)
	if err == nil {
		t.Fatal("expected error on 409, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Errorf("StatusCode = %d, want 409", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Body, "holding contended") {
		t.Errorf("Body = %q; missing server error message", apiErr.Body)
	}
}

// TestGetAcceptChoiceContext_PathEscapesContractID guards against a
// real footgun: contract IDs upstream are opaque strings that MAY
// contain `/` and `#` — path-escape them so a literal `cid#0` doesn't
// get interpreted as a path segment break or a fragment.
//
// We assert on `r.RequestURI` (the raw wire form) rather than
// `r.URL.Path` (which the stdlib transparently decodes); after
// decoding, the receiver's URL.Path looks identical to an unescaped
// version, so a missing escape would *only* be visible on the wire.
func TestGetAcceptChoiceContext_PathEscapesContractID(t *testing.T) {
	var gotRequestURI string
	var gotDecodedPath string
	c := dialAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		gotDecodedPath = r.URL.Path
		_, _ = w.Write([]byte(`{"choiceContextData":null,"disclosedContracts":[]}`))
	}))

	const cid = "cid#with/slashes:and#hashes"
	if _, err := c.GetAcceptChoiceContext(context.Background(), cid, nil, false); err != nil {
		t.Fatalf("GetAcceptChoiceContext: %v", err)
	}

	// Wire form: the `#` and `/` inside the cid MUST be percent-
	// encoded (`%23`, `%2F`). If they weren't escaped, the server
	// would see them as fragment / path-segment delimiters.
	if !strings.Contains(gotRequestURI, "%23") || !strings.Contains(gotRequestURI, "%2F") {
		t.Errorf("RequestURI %q didn't carry percent-encoded #/ delimiters", gotRequestURI)
	}
	if !strings.HasSuffix(gotRequestURI, "/choice-contexts/accept") {
		t.Errorf("RequestURI %q missing /choice-contexts/accept suffix", gotRequestURI)
	}
	// And after the stdlib decodes, the original cid must be intact
	// in the segment between the V2 prefix and the accept suffix.
	wantDecoded := "/registry/transfer-instruction/v2/" + cid + "/choice-contexts/accept"
	if gotDecodedPath != wantDecoded {
		t.Errorf("decoded path = %q, want %q", gotDecodedPath, wantDecoded)
	}
}

// TestGetAcceptChoiceContext_RejectsEmptyID — defensive: an empty
// contract ID is a caller bug, not "POST to the bare prefix".
func TestGetAcceptChoiceContext_RejectsEmptyID(t *testing.T) {
	c := dialAgainst(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("server should NOT be hit when ID is empty")
	}))
	_, err := c.GetAcceptChoiceContext(context.Background(), "", nil, false)
	if err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
	if !strings.Contains(err.Error(), "transferInstructionID is required") {
		t.Errorf("error %q should mention the missing arg", err.Error())
	}
}
