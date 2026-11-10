package splice

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
// We hash the extracted tree rather than the raw tarball because
// GitHub regenerates source-tarballs lazily and the gzip metadata can
// drift, making a raw-gzip hash unreliable.
//
// Container images: this catalogue pins the source TREE, but the ghcr
// images Splice's compose pulls are referenced by mutable tags (a single
// shared IMAGE_TAG addresses ~6 distinct images, so per-image @sha256
// pinning via the compose env is not possible). DevKit instead records
// the resolved image digests post-up and warns on drift — see
// internal/localnet/image_digests.go and docs/limitations.md ("Container
// image pinning").
type Version struct {
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`      // immutable git commit SHA the tag pointed to
	ContentSHA string `json:"content_sha"` // sha-256 of the extracted localnet subtree (sorted, path+content)
	Size       int64  `json:"size"`        // informational; used only to print a hint before download
	Major      string `json:"major"`       // routes to internal/splice/v0X adapter

	// MinMemoryBytes overrides the code default min for this entry;
	// 0 defers to MinMemoryFor. Values use decimal GB (1 GB = 10^9 bytes) so "8 GB" in Docker
	// Desktop satisfies min_memory_bytes of 8_000_000_000.
	MinMemoryBytes uint64 `json:"min_memory_bytes,omitempty"`
	// RecommendedMemoryBytes overrides the code default recommended
	// memory for this entry; 0 defers to RecommendedMemoryFor.
	RecommendedMemoryBytes uint64 `json:"recommended_memory_bytes,omitempty"`

	// Channel labels the maturity of a catalogue entry. Empty (the
	// historical default) and "stable" both mean a production-ready
	// release; "alpha" marks an opt-in pre-release.
	Channel string `json:"channel,omitempty"`

	// ImageRepo overrides the default Docker image repository for
	// this entry. Empty falls back to the stable repo that Splice's
	// compose env files default to. The adapter forwards it as the
	// `IMAGE_REPO` compose env.
	ImageRepo string `json:"image_repo,omitempty"`

	// ImageTag overrides the Docker image tag used for this entry.
	// Empty falls back to Tag. The adapter forwards it as the
	// `IMAGE_TAG` compose env.
	ImageTag string `json:"image_tag,omitempty"`
}

// IsAlpha reports whether the entry is in the alpha channel.
func (v Version) IsAlpha() bool { return v.Channel == "alpha" }

// SupportsTokenStandardV2 reports whether a Splice release includes the
// released CIP-0112 APIs and implementation. Token Standard V2 shipped in
// Splice 0.6.11; the old branch-backed catalogue alias remains supported for
// reproducibility of pre-release LocalNets.
func SupportsTokenStandardV2(tag string) bool {
	if tag == "token-standard-v2" {
		return true
	}
	v, ok := parseSemver(tag)
	if !ok {
		return false
	}
	return v[0] > 0 || (v[0] == 0 && (v[1] > 6 || (v[1] == 6 && v[2] >= 11)))
}

//go:embed versions.json
var catalogueFile []byte

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

// MinMemoryFor returns the Docker daemon memory floor for v.
// Resolution order:
//  1. v.MinMemoryBytes when the entry sets an override.
//  2. The largest MinMemoryBytes among catalogued entries that share
//     v.Major — one override covers the whole major line.
//  3. defaultMinMemoryBytesFallback (4 GB).
func MinMemoryFor(v Version) uint64 {
	if v.MinMemoryBytes > 0 {
		return v.MinMemoryBytes
	}
	if m := strictestMinForMajor(v.Major); m > 0 {
		return m
	}
	return defaultMinMemoryBytesFallback
}

// RecommendedMemoryFor returns the version's recommended memory
// (above which there are no resource warnings). Resolution order:
//  1. v.RecommendedMemoryBytes when the entry sets an override.
//  2. The largest RecommendedMemoryBytes among catalogued entries
//     that share v.Major.
//  3. defaultRecommendedMemoryBytesFallback (6 GB), raised to at
//     least MinMemoryFor(v) so the WARN tier never undercuts the
//     hard gate and never returns 0.
func RecommendedMemoryFor(v Version) uint64 {
	if v.RecommendedMemoryBytes > 0 {
		return v.RecommendedMemoryBytes
	}
	if m := strictestRecommendedForMajor(v.Major); m > 0 {
		return m
	}
	rec := defaultRecommendedMemoryBytesFallback
	if min := MinMemoryFor(v); rec < min {
		return min
	}
	return rec
}

func strictestMinForMajor(major string) uint64 {
	if major == "" {
		return 0
	}
	var max uint64
	for _, v := range SupportedVersions {
		if v.Major == major && v.MinMemoryBytes > max {
			max = v.MinMemoryBytes
		}
	}
	return max
}

func strictestRecommendedForMajor(major string) uint64 {
	if major == "" {
		return 0
	}
	var max uint64
	for _, v := range SupportedVersions {
		if v.Major == major && v.RecommendedMemoryBytes > max {
			max = v.RecommendedMemoryBytes
		}
	}
	return max
}

const defaultMinMemoryBytesFallback uint64 = 4_000_000_000

const defaultRecommendedMemoryBytesFallback uint64 = 6_000_000_000

// Supported returns the supported tags in ascending version order.
// Ordering is numeric (0.6.9 before 0.6.10), not lexical, so the CLI's
// `--version` list matches the semver-sorted Web UI picker; non-semver
// tags (e.g. token-standard-v2) sort last. Deterministic across hosts.
func Supported() []string {
	out := make([]string, 0, len(SupportedVersions))
	for k := range SupportedVersions {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return compareTags(out[i], out[j]) < 0 })
	return out
}

func compareTags(a, b string) int {
	va, oka := parseSemver(a)
	vb, okb := parseSemver(b)
	if oka && okb {
		for k := 0; k < 3; k++ {
			if va[k] != vb[k] {
				if va[k] < vb[k] {
					return -1
				}
				return 1
			}
		}
		return strings.Compare(a, b)
	}
	if oka != okb {
		if oka {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func parseSemver(tag string) ([3]int, bool) {
	var v [3]int
	base := tag
	if i := strings.IndexAny(base, "-+"); i >= 0 {
		base = base[:i]
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}
