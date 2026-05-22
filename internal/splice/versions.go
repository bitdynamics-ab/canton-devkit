package splice

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Version describes a curated, tested Splice release. DevKit only allows
// `localnet up` against versions in the catalogue so users never compose-
// up an untested upstream tag.
//
// # Integrity model
//
// The catalogue pins each tag to a git commit SHA (immutable, content-
// addressable) and to a ContentSHA over the extracted localnet subtree.
// Fetch downloads `archive/<commit-sha>.tar.gz` (not `archive/refs/tags/
// <tag>.tar.gz`) so a force-pushed tag can't quietly point at different
// code. After extraction, ContentSHA verifies the actual files match
// what was reviewed when this entry was added.
//
// The previous catalogue (pre-2026-05) also pinned the raw gzip SHA-256,
// but GitHub regenerates source-tarballs lazily and the gzip metadata
// can drift — so the gzip hash was unreliable. The commit-SHA URL +
// post-extract ContentSHA combo replaces it cleanly.
//
// # Maintainer flow
//
// Add a new entry via:
//
//	scripts/add-splice-version.sh <tag>
//
// The script resolves <tag> → commit SHA via the GitHub API, fetches +
// extracts the archive, computes ContentSHA, and appends a versions.json
// entry. A maintainer reviews the diff and commits.
type Version struct {
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`      // immutable git commit SHA the tag pointed to
	ContentSHA string `json:"content_sha"` // sha-256 of the extracted localnet subtree (sorted, path+content)
	Size       int64  `json:"size"`        // informational; used only to print a hint before download
	Major      string `json:"major"`       // routes to internal/splice/v0X adapter
}

// catalogueFile is the embedded JSON catalogue. Kept as a separate file
// (rather than literal Go code) so the maintainer refresh script can
// append entries with a tiny JSON edit instead of regenerating Go
// source.
//
//go:embed versions.json
var catalogueFile []byte

// catalogue holds the in-memory shape of versions.json after init().
var catalogue struct {
	LatestAlias string    `json:"latest_alias"`
	Versions    []Version `json:"versions"`
}

// SupportedVersions is the curated tag → Version map, populated from
// versions.json at init time. Exposed for callers that want random
// access; iteration order is unspecified — use Supported() for
// deterministic listing.
var SupportedVersions map[string]Version

// LatestAlias is the version returned when the user passes
// `--version latest` (or omits the flag). It MUST be a key in
// SupportedVersions; init() verifies this.
var LatestAlias string

func init() {
	if err := json.Unmarshal(catalogueFile, &catalogue); err != nil {
		panic(fmt.Sprintf("splice: parse embedded versions.json: %v", err))
	}
	SupportedVersions = make(map[string]Version, len(catalogue.Versions))
	for _, v := range catalogue.Versions {
		if _, dup := SupportedVersions[v.Tag]; dup {
			panic(fmt.Sprintf("splice: duplicate tag %q in versions.json", v.Tag))
		}
		if v.Commit == "" || v.ContentSHA == "" || v.Major == "" {
			panic(fmt.Sprintf("splice: incomplete entry for tag %q (commit/content_sha/major required)", v.Tag))
		}
		SupportedVersions[v.Tag] = v
	}
	LatestAlias = catalogue.LatestAlias
	if _, ok := SupportedVersions[LatestAlias]; !ok {
		panic(fmt.Sprintf("splice: latest_alias %q is not in versions list", LatestAlias))
	}
}

// Resolve maps a user-supplied version string to a curated Version.
// The special value "latest" (or "") maps to LatestAlias. Tags not in
// the catalogue return ErrUncuratedTag — the caller is responsible for
// deciding whether to fall through to runtime resolution (see
// ResolveOrUpstream / ResolveUpstream).
//
// We keep Resolve catalogue-only so a "just give me whatever works"
// caller can't accidentally start running arbitrary upstream tags
// without an explicit opt-in elsewhere.
func Resolve(req string) (Version, error) {
	if req == "" || req == "latest" {
		req = LatestAlias
	}
	v, ok := SupportedVersions[req]
	if !ok {
		return Version{}, fmt.Errorf("%w: %q (curated tags: %s)",
			ErrUncuratedTag, req, strings.Join(Supported(), ", "))
	}
	return v, nil
}

// Supported returns the supported tags sorted ascending. Stable order
// makes it easy to grep error messages and renders the version list
// deterministically across hosts.
func Supported() []string {
	out := make([]string, 0, len(SupportedVersions))
	for k := range SupportedVersions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
