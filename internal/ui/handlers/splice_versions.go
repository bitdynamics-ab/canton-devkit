package handlers

import (
	"context"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// upstreamCache wraps splice.CrossReferenceUpstream with a small
// TTL cache so the frontend's create-modal version picker can
// poll without hammering GitHub's API (which has a 60 req/hr
// unauthenticated limit). On cache miss OR forced refresh, we
// re-fetch; on network failure we degrade to the catalogue-only
// view rather than 500-ing.
//
// The mutex is held only briefly around the cache read/write;
// the actual fetch happens outside the lock so concurrent
// requests during a refresh see the stale value rather than
// queueing behind a slow GitHub call.
type upstreamCache struct {
	mu      sync.RWMutex
	rows    []splice.VersionStatus
	fetched time.Time
	ttl     time.Duration
}

// upstreamCacheTTL is how long a successful CrossReferenceUpstream
// response stays valid. 5 minutes balances "fresh enough for the
// version picker" against GitHub's rate limit. The frontend's
// poll interval is 3-15s depending on screen; a 5min TTL means
// at most 12 GitHub hits per hour per UI session.
const upstreamCacheTTL = 5 * time.Minute

// upstreamFetchTimeout caps a single CrossReferenceUpstream call.
// GitHub's API typically responds in <500ms; we give 10s headroom
// for transient slowness without blocking the HTTP request loop.
const upstreamFetchTimeout = 10 * time.Second

var versionsCache = &upstreamCache{ttl: upstreamCacheTTL}

// fetchUpstreamRows returns the most recent CrossReferenceUpstream
// rows, refreshing the cache if (a) it's empty, (b) older than TTL,
// or (c) the caller passed force=true. On fetch failure, returns
// the stale cached rows plus the error so the handler can render
// degraded.
func (c *upstreamCache) fetchUpstreamRows(ctx context.Context, force bool) ([]splice.VersionStatus, bool, error) {
	c.mu.RLock()
	fresh := !c.fetched.IsZero() && time.Since(c.fetched) < c.ttl
	cached := c.rows
	c.mu.RUnlock()
	if fresh && !force {
		return cached, true, nil
	}

	// Refresh outside the lock. Another concurrent request may
	// also hit this path; that's fine — both publish their
	// result. Last writer wins; the cost is two GitHub calls
	// instead of one in the rare concurrent-refresh case.
	fetchCtx, cancel := context.WithTimeout(ctx, upstreamFetchTimeout)
	defer cancel()
	rows, err := splice.CrossReferenceUpstream(fetchCtx)
	if err != nil {
		return cached, false, err
	}
	c.mu.Lock()
	c.rows = rows
	c.fetched = time.Now()
	c.mu.Unlock()
	return rows, true, nil
}

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
	// UpstreamFetched (BIT-171) is true when the response was
	// enriched with live GitHub data, false when we fell back to
	// the catalogue-only view (network failure, GitHub rate
	// limit). Frontend renders a small "live data" / "cached"
	// indicator off this so users know the available/drifted
	// signals are current.
	UpstreamFetched bool `json:"upstream_fetched"`
	// UpstreamWarning carries a one-line human-readable cause
	// when UpstreamFetched=false. Empty when fetch succeeded or
	// when the fallback is silent (no prior cache, ?offline=true).
	UpstreamWarning string `json:"upstream_warning,omitempty"`
}

// handleSpliceVersions: GET /api/splice/versions → SpliceVersionsResponse.
//
// BIT-171: enriched with upstream cross-reference data. The
// handler now calls splice.CrossReferenceUpstream (cached for
// upstreamCacheTTL) to surface the four-way status taxonomy from
// the JSX mockup: supported / latest / drifted / available /
// catalogued-only.
//
// Query parameters:
//
//	?refresh=true  force a fresh upstream fetch (bypasses cache)
//	?offline=true  skip upstream entirely; catalogue-only view
//
// Failure modes degrade rather than 500:
//
//	upstream unreachable → falls back to catalogue-only with a
//	                       UpstreamWarning + upstream_fetched=false
//	bad query value      → 400 INVALID_REQUEST
//
// Sort order: descending by tag (newest first) using a small
// dotted-version comparator so the picker shows latest at top.
func handleSpliceVersions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offline := q.Get("offline") == "true"
	force := q.Get("refresh") == "true"

	if offline {
		writeJSON(w, http.StatusOK, buildCatalogueOnlyResponse(""))
		return
	}

	rows, ok, err := versionsCache.fetchUpstreamRows(r.Context(), force)
	if !ok && err != nil {
		// Upstream fetch failed AND we had no cache to fall back
		// on. Render catalogue-only with a warning the frontend
		// can surface (e.g. "GitHub rate-limited, retry in 60s").
		log.Printf("splice versions: upstream fetch failed (degraded to catalogue): %v", err)
		writeJSON(w, http.StatusOK,
			buildCatalogueOnlyResponse("upstream lookup failed: "+err.Error()))
		return
	}

	entries := mapStatusRowsToEntries(rows)
	resp := SpliceVersionsResponse{
		SchemaVersion:   types.SchemaVersion,
		LatestAlias:     splice.LatestAlias,
		Versions:        entries,
		UpstreamFetched: ok && err == nil,
	}
	if err != nil {
		// Soft failure: cache hit but the refresh attempt errored.
		// Surface as a warning so the UI can flag "data may be
		// stale" without losing the rendered list.
		resp.UpstreamWarning = "upstream refresh failed (showing cached): " + err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// mapStatusRowsToEntries adapts the internal-flavored
// splice.VersionStatus rows to the API-stable SpliceVersionEntry
// shape. The `Status` string is the public wire token the
// frontend's version picker switches on — keep stable across
// versions.
//
// "latest" is a SECOND label on top of "supported" — the
// LatestAlias tag is marked "latest" so the frontend can render
// its distinct "latest" pill without re-deriving from the
// LatestAlias field.
func mapStatusRowsToEntries(rows []splice.VersionStatus) []SpliceVersionEntry {
	out := make([]SpliceVersionEntry, 0, len(rows))
	for _, r := range rows {
		entry := SpliceVersionEntry{
			Tag:    r.Tag,
			Major:  r.Major,
			Commit: r.CataloguedCommit,
		}
		switch r.Status {
		case splice.StatusSupported:
			entry.Status = "supported"
		case splice.StatusDrifted:
			entry.Status = "drifted"
			entry.Commit = r.CataloguedCommit
			entry.Note = "catalogue pin " + short(r.CataloguedCommit) +
				" differs from upstream " + short(r.UpstreamCommit) +
				" — re-review before re-using this tag"
		case splice.StatusAvailable:
			entry.Status = "available"
			entry.Commit = r.UpstreamCommit
			entry.Note = "run scripts/add-splice-version.sh " + r.Tag +
				" to adopt"
		case splice.StatusCataloguedOnly:
			entry.Status = "catalogued-only"
			entry.Note = "upstream no longer has this tag — kept for reproducibility"
		default:
			entry.Status = "supported"
		}
		if r.Tag == splice.LatestAlias {
			// Latest wins over supported but NOT over drifted
			// (a drifted "latest" tag is more important to flag
			// than to label as latest — user needs to know it's
			// drifted before picking it).
			if entry.Status == "supported" {
				entry.Status = "latest"
			}
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return compareVersionTagsDesc(out[i].Tag, out[j].Tag)
	})
	return out
}

// buildCatalogueOnlyResponse renders the response without any
// upstream enrichment. Used by ?offline=true and as the fallback
// when upstream is unreachable AND we have no cache.
func buildCatalogueOnlyResponse(warning string) SpliceVersionsResponse {
	entries := make([]SpliceVersionEntry, 0, len(splice.SupportedVersions))
	for tag, v := range splice.SupportedVersions {
		entry := SpliceVersionEntry{
			Tag:    tag,
			Status: "supported",
			Major:  v.Major,
			Commit: v.Commit,
		}
		if tag == splice.LatestAlias {
			entry.Status = "latest"
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return compareVersionTagsDesc(entries[i].Tag, entries[j].Tag)
	})
	return SpliceVersionsResponse{
		SchemaVersion:   types.SchemaVersion,
		LatestAlias:     splice.LatestAlias,
		Versions:        entries,
		UpstreamFetched: false,
		UpstreamWarning: warning,
	}
}

// short returns the first 8 chars of a commit SHA. Same convention
// as the CLI `localnet versions` renderer — used in Note text so
// the wire payload stays compact (8 chars is enough for human
// identification, full SHA is in Commit).
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
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
