package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// TestSpliceVersions_ListsCuratedCatalogue pins the basic contract:
// every entry in splice.SupportedVersions appears in the response,
// and the count matches.
func TestSpliceVersions_ListsCuratedCatalogue(t *testing.T) {
	mux := http.NewServeMux()
	MountSpliceVersions(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/splice/versions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp SpliceVersionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Versions) != len(splice.SupportedVersions) {
		t.Errorf("versions length = %d, want %d (one per curated entry)",
			len(resp.Versions), len(splice.SupportedVersions))
	}

	gotTags := make(map[string]SpliceVersionEntry, len(resp.Versions))
	for _, v := range resp.Versions {
		gotTags[v.Tag] = v
	}
	for tag, v := range splice.SupportedVersions {
		got, ok := gotTags[tag]
		if !ok {
			t.Errorf("missing entry for tag %q", tag)
			continue
		}
		if got.Commit != v.Commit {
			t.Errorf("tag %q: commit = %q, want %q", tag, got.Commit, v.Commit)
		}
		if got.Major != v.Major {
			t.Errorf("tag %q: major = %q, want %q", tag, got.Major, v.Major)
		}
	}
}

// TestSpliceVersions_LatestAliasFlaggedSeparately pins the
// status taxonomy: the LatestAlias entry MUST carry status="latest",
// every other curated tag MUST carry "supported". The frontend
// renders a distinct "latest" pill that depends on this.
func TestSpliceVersions_LatestAliasFlaggedSeparately(t *testing.T) {
	mux := http.NewServeMux()
	MountSpliceVersions(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/splice/versions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp SpliceVersionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.LatestAlias != splice.LatestAlias {
		t.Errorf("latest_alias = %q, want %q", resp.LatestAlias, splice.LatestAlias)
	}

	for _, v := range resp.Versions {
		if v.Tag == splice.LatestAlias {
			if v.Status != "latest" {
				t.Errorf("latest tag %q status = %q, want 'latest'", v.Tag, v.Status)
			}
		} else {
			if v.Status != "supported" {
				t.Errorf("non-latest tag %q status = %q, want 'supported'", v.Tag, v.Status)
			}
		}
	}
}

// TestSpliceVersions_SortedDescendingNewestFirst pins the picker's
// visual order. The mockup shows the latest at the top — the
// frontend doesn't re-sort, so the API order is what users see.
//
// Uses synthetic input via compareVersionTagsDesc directly so
// the test doesn't depend on whatever versions ship in
// versions.json today.
func TestSpliceVersions_SortedDescendingNewestFirst(t *testing.T) {
	cases := []struct {
		a, b string
		want bool // true if a should come before b
	}{
		{"0.6.4", "0.6.3", true},     // same major, patch differs
		{"0.6.4", "0.5.18", true},    // major differs (don't compare lexically: 0.5.18 > 0.6.4 lex would be wrong)
		{"0.4.12", "0.4.2", true},    // 12 > 2 numerically
		{"0.6.10", "0.6.9", true},    // 10 > 9 numerically
		{"0.6.0", "0.5.99", true},    // major beats minor
		{"0.6.4", "0.6.4", false},    // equal → not before
		{"0.5.3", "0.6.0", false},    // 0.5.x is older
	}
	for _, tc := range cases {
		got := compareVersionTagsDesc(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("compareVersionTagsDesc(%q, %q) = %v, want %v",
				tc.a, tc.b, got, tc.want)
		}
	}
}

// TestSpliceVersions_CarriesSchemaVersion pins the cross-language
// handshake. Every /api/* response carries schema_version; without
// it the bundle-vs-server check can't detect drift.
func TestSpliceVersions_CarriesSchemaVersion(t *testing.T) {
	mux := http.NewServeMux()
	MountSpliceVersions(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/splice/versions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["schema_version"]; !ok {
		t.Error("response missing schema_version field")
	}
}
