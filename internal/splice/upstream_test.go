package splice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListUpstreamTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirror the actual GitHub response shape.
		payload := []map[string]any{
			{"name": "0.6.4", "commit": map[string]string{"sha": "578b7822d62947763a48334d556aefebc7ffacec"}},
			{"name": "0.6.3", "commit": map[string]string{"sha": "6d50c039641736b6df5358af2b1fc6c123f28e18"}},
			{"name": "0.5.18", "commit": map[string]string{"sha": "b162650cd18df5b96dddeffd9bd48be5b2ff37d7"}},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()
	prev := upstreamTagsEndpoint
	upstreamTagsEndpoint = srv.URL
	defer func() { upstreamTagsEndpoint = prev }()

	got, err := ListUpstreamTags(context.Background())
	if err != nil {
		t.Fatalf("ListUpstreamTags: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(got))
	}
	// Descending order.
	if got[0].Tag != "0.6.4" {
		t.Errorf("expected newest first; got[0]=%v", got[0])
	}
	if got[0].Commit != "578b7822d62947763a48334d556aefebc7ffacec" {
		t.Errorf("commit SHA not captured: %v", got[0])
	}
}

func TestListUpstreamTags_HandlesRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer srv.Close()
	prev := upstreamTagsEndpoint
	upstreamTagsEndpoint = srv.URL
	defer func() { upstreamTagsEndpoint = prev }()

	_, err := ListUpstreamTags(context.Background())
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error should mention rate limiting, got %v", err)
	}
}

func TestCrossReferenceUpstream(t *testing.T) {
	// Mock upstream: 0.6.4 (matches catalogue), 0.6.3 (catalogued but
	// drifted to a different commit), 0.7.0-new (available but not
	// catalogued). Catalogued 0.5.18 missing from upstream → catalogued-only.
	upstreamPayload := []map[string]any{
		{"name": "0.7.0-new", "commit": map[string]string{"sha": "ffffffffffffffffffffffffffffffffffffffff"}},
		{"name": "0.6.4", "commit": map[string]string{"sha": SupportedVersions["0.6.4"].Commit}},
		{"name": "0.6.3", "commit": map[string]string{"sha": "0000000000000000000000000000000000000000"}}, // drifted
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(upstreamPayload)
	}))
	defer srv.Close()
	prev := upstreamTagsEndpoint
	upstreamTagsEndpoint = srv.URL
	defer func() { upstreamTagsEndpoint = prev }()

	got, err := CrossReferenceUpstream(context.Background())
	if err != nil {
		t.Fatalf("CrossReferenceUpstream: %v", err)
	}
	byTag := make(map[string]VersionStatus)
	for _, s := range got {
		byTag[s.Tag] = s
	}

	if s := byTag["0.6.4"]; s.Status != StatusSupported {
		t.Errorf("0.6.4 should be supported, got %v", s.Status)
	}
	if s := byTag["0.6.3"]; s.Status != StatusDrifted {
		t.Errorf("0.6.3 should be drifted (upstream moved), got %v", s.Status)
	}
	if s := byTag["0.7.0-new"]; s.Status != StatusAvailable {
		t.Errorf("0.7.0-new should be available, got %v", s.Status)
	}
	if s := byTag["0.5.18"]; s.Status != StatusCataloguedOnly {
		t.Errorf("0.5.18 should be catalogued-only (missing upstream), got %v", s.Status)
	}
}
