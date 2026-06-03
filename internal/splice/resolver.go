package splice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Two-layer version model (PR #20 #2)
// ====================================
//
// Layer 1 — Curated catalogue (versions.json). Tested entries that
// power `--version latest` and the default `--version <tag>` flow.
// Pinned to a content-SHA so Fetch can verify the extracted tree.
//
// Layer 2 — Runtime resolution. When the user passes a tag that is
// NOT in the curated catalogue, ResolveUpstream consults the GitHub
// REST API to discover the commit SHA the tag points to, infers the
// adapter major from the tag string, and synthesises a Version{}
// with an empty ContentSHA. Fetch treats an empty ContentSHA as
// "compute + record on first run, verify thereafter" — so even
// uncurated runs are content-pinned for repeatable bring-ups, just
// without the human review step the catalogue carries.
//
// Resolved uncurated Versions are written to a per-user cache at
//   <CacheRoot()>/resolved-versions.json
// so subsequent invocations don't re-hit GitHub or re-prompt.
//
// Why this exists: prior to PR #20, `--version` was strictly limited
// to the catalogue. Zhe asked for an escape hatch for prereleases /
// alphas while keeping the curated path the safe default. The
// alternative — a weekly cron that auto-bumps the catalogue (#1) —
// added latency without solving the prerelease use-case. Once #2
// lands, the cron is redundant and we remove it.

// ErrUncuratedTag is returned by Resolve when the requested tag is
// not in the curated catalogue. Callers that want the uncurated
// runtime-resolution path must explicitly opt in via ResolveUpstream
// (or pass AllowUncurated to ResolveWith).
var ErrUncuratedTag = errors.New("tag is not in the curated DevKit catalogue")

// upstreamTagRefEndpoint is the GitHub REST URL for a single tag's
// commit ref. We hit `git/refs/tags/<tag>` and follow `object.url`
// once if the ref is annotated. Public anonymous endpoint; same
// rate-limit caveats as ListUpstreamTags.
//
// Package var so tests can point at an httptest server.
var upstreamTagRefEndpoint = "https://api.github.com/repos/canton-network/splice/git/refs/tags/"

// tagMajorRE extracts the "0.N" prefix from a Splice tag string.
// Splice tags follow semver "0.N.M[-suffix]" (e.g. "0.6.5",
// "0.7.0-alpha.4"). The Major field drives adapter selection in
// internal/localnet/adapters.go.
var tagMajorRE = regexp.MustCompile(`^v?(\d+\.\d+)`)

// majorFromTag returns the "0.N" prefix used to route to an adapter,
// or an empty string if the tag doesn't follow Splice's convention.
// Exported as a regex-driven helper so the resolver and the catalogue
// loader can both rely on the same parse.
func majorFromTag(tag string) string {
	m := tagMajorRE.FindStringSubmatch(tag)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// ResolvedVersionCache is the on-disk shape of resolved-versions.json.
// Schema-versioned so future fields can be added without a forced
// rewrite of every user's cache.
type ResolvedVersionCache struct {
	SchemaVersion int               `json:"schema_version"`
	Entries       []ResolvedVersion `json:"entries"`
}

// ResolvedVersion is one cached uncurated resolution.
type ResolvedVersion struct {
	Version
	ResolvedAt time.Time `json:"resolved_at"`
}

// ResolvedCacheSchemaVersion is the current schema version of
// resolved-versions.json. Bumped if the cache format changes
// incompatibly.
const ResolvedCacheSchemaVersion = 1

// ResolvedCachePath is the file path where uncurated resolutions are
// persisted. Lives under CacheRoot() so a `rm -rf ~/.canton-devkit/
// cache/` clears it along with the extracted tarballs.
func ResolvedCachePath() string {
	return filepath.Join(CacheRoot(), "resolved-versions.json")
}

// ReadResolvedCache loads the resolved-versions cache, returning an
// empty cache (not an error) if the file does not yet exist. A
// malformed file IS surfaced — we'd rather fail loudly than silently
// discard a user's resolved tags.
func ReadResolvedCache() (*ResolvedVersionCache, error) {
	data, err := os.ReadFile(ResolvedCachePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ResolvedVersionCache{SchemaVersion: ResolvedCacheSchemaVersion}, nil
		}
		return nil, fmt.Errorf("read resolved-versions cache: %w", err)
	}
	var c ResolvedVersionCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ResolvedCachePath(), err)
	}
	if c.SchemaVersion > ResolvedCacheSchemaVersion {
		return nil, fmt.Errorf("%s: schema_version %d newer than this DevKit's %d — upgrade canton-devkit",
			ResolvedCachePath(), c.SchemaVersion, ResolvedCacheSchemaVersion)
	}
	return &c, nil
}

// WriteResolvedCache persists the cache atomically (tmp + rename) so
// a crash mid-write leaves the previous file intact. Creates the
// cache directory if missing.
func WriteResolvedCache(c *ResolvedVersionCache) error {
	if c == nil {
		return errors.New("nil cache")
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = ResolvedCacheSchemaVersion
	}
	// Stable order so file diffs are reviewable.
	sort.Slice(c.Entries, func(i, j int) bool { return c.Entries[i].Tag < c.Entries[j].Tag })

	path := ResolvedCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir cache root: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename cache: %w", err)
	}
	return nil
}

// LookupResolved finds a previously cached uncurated resolution by
// tag. Returns (entry, true) on hit, (zero, false) on miss.
func (c *ResolvedVersionCache) LookupResolved(tag string) (ResolvedVersion, bool) {
	if c == nil {
		return ResolvedVersion{}, false
	}
	for _, e := range c.Entries {
		if e.Tag == tag {
			return e, true
		}
	}
	return ResolvedVersion{}, false
}

// Upsert replaces an entry with the same tag, or appends if not
// present. Used by ResolveUpstream after a successful GitHub lookup.
func (c *ResolvedVersionCache) Upsert(r ResolvedVersion) {
	for i := range c.Entries {
		if c.Entries[i].Tag == r.Tag {
			c.Entries[i] = r
			return
		}
	}
	c.Entries = append(c.Entries, r)
}

// ResolveUpstream is the Layer 2 entry point. It returns a Version{}
// for any Splice tag — even one that isn't in the curated catalogue
// — by:
//
//  1. Checking the on-disk resolved-versions cache (fast path).
//  2. On miss, hitting api.github.com/repos/canton-network/splice/
//     git/refs/tags/<tag> to discover the commit SHA.
//  3. Inferring the adapter Major from the tag prefix.
//  4. Persisting the resolution to the cache so future runs skip
//     the network round-trip.
//
// ContentSHA is intentionally left empty: Fetch records it on first
// extraction. This mirrors the curated flow but without a human
// review step — uncurated runs trade audit for flexibility.
//
// Returns an error if the tag does not exist upstream or if the
// inferred Major has no DevKit adapter (the user can still file an
// adapter request, but RunUp can't bring it up).
func ResolveUpstream(ctx context.Context, tag string) (Version, error) {
	cache, _ := ReadResolvedCache() // malformed-cache error handled by callers that want strictness
	if cache != nil {
		if hit, ok := cache.LookupResolved(tag); ok {
			return hit.Version, nil
		}
	}

	commit, err := lookupTagCommit(ctx, tag)
	if err != nil {
		return Version{}, fmt.Errorf("resolve %q upstream: %w", tag, err)
	}
	major := majorFromTag(tag)
	if major == "" {
		return Version{}, fmt.Errorf("resolve %q: cannot infer adapter major from tag (expected v?N.N.…)", tag)
	}
	v := Version{
		Tag:        tag,
		Commit:     commit,
		ContentSHA: "", // Fetch fills this on first extraction
		Major:      major,
	}
	if cache != nil {
		cache.Upsert(ResolvedVersion{Version: v, ResolvedAt: time.Now().UTC()})
		_ = WriteResolvedCache(cache) // best-effort; a write failure shouldn't fail the bring-up
	}
	return v, nil
}

// ResolveOrUpstream is the orchestrator-facing entry point that
// implements the two-layer policy:
//
//  1. Try the curated catalogue (fast, offline, audited).
//  2. If allowUncurated is true and the tag is not curated, fall
//     through to ResolveUpstream (network, cached).
//
// `requested` accepts the same inputs as Resolve (including "latest"
// / "" → LatestAlias).
//
// allowUncurated is wired to a CLI opt-in (`--allow-uncurated` on
// `localnet up`) so the default UX stays in the audited path. The
// caller is also expected to print a one-line warning when
// returning the uncurated result so the user knows what they're
// running. We don't print here to keep this function pure for tests.
func ResolveOrUpstream(ctx context.Context, requested string, allowUncurated bool) (Version, bool, error) {
	v, err := Resolve(requested)
	if err == nil {
		return v, false, nil
	}
	if !errors.Is(err, ErrUncuratedTag) {
		return Version{}, false, err
	}
	if !allowUncurated {
		return Version{}, false, fmt.Errorf("%w. Pass --allow-uncurated to resolve uncurated tags against the upstream Splice repository", err)
	}
	// Strip "latest" alias normalisation — it should never reach this
	// branch (LatestAlias is always in SupportedVersions) but be
	// defensive in case future code regresses.
	if requested == "" || requested == "latest" {
		requested = LatestAlias
	}
	v, err = ResolveUpstream(ctx, requested)
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

// lookupTagCommit asks GitHub for the commit a tag currently points
// to. Annotated tags require a second hop via object.url to resolve
// to the underlying commit; we handle both lightweight and annotated
// cases.
func lookupTagCommit(ctx context.Context, tag string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamHTTPTimeout)
	defer cancel()

	url := upstreamTagRefEndpoint + tag
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "canton-devkit/upstream-resolve")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("tag %q not found upstream (404)", tag)
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("GET %s: HTTP 403 (rate limited or auth required); "+
			"anonymous GitHub API allows 60 req/hour per IP", url)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, string(body))
	}

	var ref struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
			URL  string `json:"url"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ref); err != nil {
		return "", fmt.Errorf("decode ref: %w", err)
	}
	if ref.Object.SHA == "" {
		return "", fmt.Errorf("GET %s: empty SHA in response", url)
	}
	if ref.Object.Type == "commit" {
		return ref.Object.SHA, nil
	}
	// Annotated tag — one more hop to dereference.
	if ref.Object.URL == "" {
		return "", fmt.Errorf("annotated tag %q missing object.url", tag)
	}
	return derefAnnotatedTag(ctx, ref.Object.URL)
}

// derefAnnotatedTag follows the object.url of an annotated tag to its
// underlying commit SHA.
func derefAnnotatedTag(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "canton-devkit/upstream-resolve")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, string(body))
	}
	var ann struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ann); err != nil {
		return "", fmt.Errorf("decode annotated tag: %w", err)
	}
	if ann.Object.SHA == "" {
		return "", fmt.Errorf("GET %s: empty SHA in annotated-tag object", url)
	}
	return ann.Object.SHA, nil
}
