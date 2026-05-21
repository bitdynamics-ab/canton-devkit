package splice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// UpstreamTag is one entry from GitHub's tag-listing API for the Splice
// repository. We surface Tag (the human name) + Commit (the immutable
// SHA the tag currently points to) and ignore the rest.
type UpstreamTag struct {
	Tag    string `json:"name"`
	Commit string // populated from the nested object{}.sha
}

// upstreamTagsEndpoint is the GitHub REST URL the discovery layer hits.
// Package var so tests can point it at an httptest server.
//
// Public anonymous endpoint; rate-limited to 60 req/hour per IP. We
// honour Last-Modified / ETag in callers if/when that becomes an
// issue, but a single CLI session never approaches the budget.
var upstreamTagsEndpoint = "https://api.github.com/repos/canton-network/splice/tags"

// upstreamHTTPTimeout caps the total round trip. Discovery is best-
// effort and the user should not wait minutes for a hung response —
// catalogue lookup still works offline.
var upstreamHTTPTimeout = 10 * time.Second

// ListUpstreamTags fetches the current set of upstream Splice tags
// from the GitHub REST API.
//
// Returns tags sorted descending (newest first) — matching GitHub's
// natural ordering for `git tag --sort=-version:refname`. Caller may
// re-sort if it needs ascending.
//
// Errors are wrapped with the endpoint URL so a user with no network
// (or behind a corporate proxy that blocks api.github.com) gets a
// clear hint.
func ListUpstreamTags(ctx context.Context) ([]UpstreamTag, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamTagsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Polite UA so GitHub can identify DevKit traffic if it ever needs
	// to differentiate (e.g. for rate-limit incident response).
	req.Header.Set("User-Agent", "canton-devkit/upstream-discovery")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", upstreamTagsEndpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		// GitHub returns 403 (not 429) when the anonymous rate limit
		// is exhausted; give the user the actionable hint.
		return nil, fmt.Errorf("GET %s: HTTP 403 (rate limited or auth required); "+
			"anonymous GitHub API allows 60 req/hour per IP", upstreamTagsEndpoint)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %s: HTTP %d: %s",
			upstreamTagsEndpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// GitHub's payload is:
	//   [{"name": "0.6.4", "commit": {"sha": "578b78...", "url": "..."}, ...}, ...]
	// We capture just the bits we use.
	var raw []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	out := make([]UpstreamTag, 0, len(raw))
	for _, e := range raw {
		out = append(out, UpstreamTag{Tag: e.Name, Commit: e.Commit.SHA})
	}
	// Stable descending order by tag string — works as a coarse
	// "newest first" since Splice uses N.N.N semver. Callers that
	// need strict semver ordering can sort themselves.
	sort.Slice(out, func(i, j int) bool { return out[i].Tag > out[j].Tag })
	return out, nil
}

// CatalogueStatus describes one tag's relationship with the catalogue.
type CatalogueStatus int

const (
	// StatusSupported: tag is in the catalogue, pin matches upstream.
	StatusSupported CatalogueStatus = iota
	// StatusDrifted: tag is in the catalogue but the commit pin no
	// longer matches the current upstream tag — the upstream tag was
	// force-moved. Security-relevant; the catalogue entry should be
	// re-reviewed.
	StatusDrifted
	// StatusAvailable: tag exists upstream but is not catalogued.
	StatusAvailable
	// StatusCataloguedOnly: tag is catalogued but no longer present
	// upstream (rare — would only happen if the tag was deleted).
	StatusCataloguedOnly
)

// String for CLI output.
func (s CatalogueStatus) String() string {
	switch s {
	case StatusSupported:
		return "supported"
	case StatusDrifted:
		return "drifted"
	case StatusAvailable:
		return "available"
	case StatusCataloguedOnly:
		return "catalogued-only"
	}
	return "unknown"
}

// MarshalJSON emits the human-readable status name instead of the
// underlying iota, so `dpm localnet versions --format=json` consumers
// don't have to maintain the int → string mapping themselves.
func (s CatalogueStatus) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// VersionStatus pairs a tag with its status + the relevant SHAs for
// diagnosis. Returned by CrossReferenceUpstream so a CLI can render
// the full picture in one call.
type VersionStatus struct {
	Tag              string
	Status           CatalogueStatus
	CataloguedCommit string // empty if not catalogued
	UpstreamCommit   string // empty if not upstream
	Major            string // catalogue's adapter major, empty if not catalogued
}

// CrossReferenceUpstream computes the catalogue-vs-upstream diff. Used
// by `dpm localnet versions` and the maintainer refresh script.
func CrossReferenceUpstream(ctx context.Context) ([]VersionStatus, error) {
	upstream, err := ListUpstreamTags(ctx)
	if err != nil {
		return nil, err
	}
	byTag := make(map[string]string, len(upstream))
	for _, u := range upstream {
		byTag[u.Tag] = u.Commit
	}
	seen := make(map[string]bool)
	var out []VersionStatus
	for tag, v := range SupportedVersions {
		seen[tag] = true
		ucommit, ok := byTag[tag]
		switch {
		case !ok:
			out = append(out, VersionStatus{Tag: tag, Status: StatusCataloguedOnly, CataloguedCommit: v.Commit, Major: v.Major})
		case ucommit != v.Commit:
			out = append(out, VersionStatus{Tag: tag, Status: StatusDrifted, CataloguedCommit: v.Commit, UpstreamCommit: ucommit, Major: v.Major})
		default:
			out = append(out, VersionStatus{Tag: tag, Status: StatusSupported, CataloguedCommit: v.Commit, UpstreamCommit: ucommit, Major: v.Major})
		}
	}
	for _, u := range upstream {
		if seen[u.Tag] {
			continue
		}
		out = append(out, VersionStatus{Tag: u.Tag, Status: StatusAvailable, UpstreamCommit: u.Commit})
	}
	// Descending by tag for a "newest first" listing.
	sort.Slice(out, func(i, j int) bool { return out[i].Tag > out[j].Tag })
	return out, nil
}
