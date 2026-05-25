package handlers

import (
	"net/http"
	"sort"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// MountSpliceVersions installs the GET /api/splice/versions route.
// Stateless (read-only from the embedded versions.json); no hub
// or other shared resource needed.
func MountSpliceVersions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/splice/versions", handleSpliceVersions)
}

// SpliceVersionEntry is the per-row shape the webui-create.jsx
// version picker renders. Mirrors the columns visible in the
// mockup: Tag · Status · Major · Commit · Note.
//
// Status taxonomy (matches the mock's color-coded badges):
//
//	"supported"        — in the curated catalogue; safe default
//	"latest"           — supported AND the current LatestAlias
//	"available"        — known upstream but not catalogued (needs
//	                     `scripts/add-splice-version.sh <tag>`)
//	"drifted"          — catalogued, but the cached commit no
//	                     longer matches the upstream tag (re-review)
//	"catalogued-only"  — in the catalogue but no longer present
//	                     upstream (upstream deleted the tag)
//
// This first slice (BIT-163f) only surfaces "supported" + "latest"
// because the available/drifted/catalogued-only states require a
// live GitHub API call from the handler — that's deferred to
// BIT-169 (needs caching + rate-limit + network-error handling).
// The shape is forward-compatible: when the upstream check lands,
// it just sets Status on entries that need it.
type SpliceVersionEntry struct {
	Tag    string `json:"tag"`
	Status string `json:"status"`
	Major  string `json:"major"`
	Commit string `json:"commit"`
	// Note carries a human remediation hint when Status indicates
	// the tag needs attention. Empty for "supported"/"latest".
	Note string `json:"note,omitempty"`
}

// SpliceVersionsResponse is the GET response envelope. Includes
// schema_version so the frontend's bundle-vs-server handshake
// applies here as it does for every other /api/* response.
//
// latest_alias is surfaced so the frontend can mark a row with
// the "latest" pill without re-deriving it from the entries
// list — avoids a frontend-side filter that could disagree with
// the server's `splice.LatestAlias` constant.
type SpliceVersionsResponse struct {
	SchemaVersion int                  `json:"schema_version"`
	LatestAlias   string               `json:"latest_alias"`
	Versions      []SpliceVersionEntry `json:"versions"`
}

// handleSpliceVersions: GET /api/splice/versions → SpliceVersionsResponse.
//
// Read-only; no path params; no query params today (BIT-169 may
// add ?refresh=true to force an upstream re-check). Output is
// stable across calls for the lifetime of the binary — the
// catalogue is baked in via go:embed at build time.
//
// Sort order is descending-by-tag (newest first) using a tiny
// dotted-version comparator. The mockup shows latest at the top
// of the picker.
func handleSpliceVersions(w http.ResponseWriter, _ *http.Request) {
	entries := make([]SpliceVersionEntry, 0, len(splice.SupportedVersions))
	for tag, v := range splice.SupportedVersions {
		entry := SpliceVersionEntry{
			Tag:    tag,
			Status: "supported",
			Major:  v.Major,
			Commit: v.Commit,
		}
		if tag == splice.LatestAlias {
			// Mark the latest separately so the frontend can
			// render the "latest" pill the mockup specifies.
			entry.Status = "latest"
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return compareVersionTagsDesc(entries[i].Tag, entries[j].Tag)
	})
	writeJSON(w, http.StatusOK, SpliceVersionsResponse{
		SchemaVersion: types.SchemaVersion,
		LatestAlias:   splice.LatestAlias,
		Versions:      entries,
	})
}

// compareVersionTagsDesc returns true if a > b under
// semantic-ish ordering — splits on '.' and compares numeric
// segments. Non-numeric segments fall back to lexicographic.
//
// Deliberately tiny — pulling a full semver package for one
// sort would be overkill, and the catalogue uses regular
// "0.4.12" / "0.6.4" style tags so a four-line splitter is
// enough.
//
// Examples (a vs b → result):
//
//	"0.6.4"  vs "0.6.3"  → true  (4 > 3)
//	"0.6.4"  vs "0.5.18" → true  (6 > 5; doesn't compare lexically)
//	"0.4.12" vs "0.4.2"  → true  (12 > 2; numeric, not lex)
//	"0.6.10" vs "0.6.9"  → true  (10 > 9; numeric)
func compareVersionTagsDesc(a, b string) bool {
	aSeg := splitDot(a)
	bSeg := splitDot(b)
	for i := 0; i < len(aSeg) && i < len(bSeg); i++ {
		an, aIsNum := parseUint(aSeg[i])
		bn, bIsNum := parseUint(bSeg[i])
		if aIsNum && bIsNum {
			if an != bn {
				return an > bn
			}
			continue
		}
		if aSeg[i] != bSeg[i] {
			return aSeg[i] > bSeg[i]
		}
	}
	// Equal prefix: longer one wins.
	return len(aSeg) > len(bSeg)
}

func splitDot(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func parseUint(s string) (uint64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, true
}
