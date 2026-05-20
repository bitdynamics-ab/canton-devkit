package splice

import (
	"fmt"
	"sort"
	"strings"
)

// Version describes a curated, tested Splice release. DevKit only allows
// `localnet up` against versions in this map so users never compose-up an
// untested upstream tag.
//
// Adding a new entry requires:
//
//  1. Downloading the upstream tag's source tarball from
//     https://github.com/canton-network/splice/archive/refs/tags/<tag>.tar.gz
//  2. Running `shasum -a 256` and recording the digest as `SHA256`.
//  3. Letting Fetch extract the tarball locally, then computing the
//     authoritative `ContentSHA` via `scripts/compute-tree-sha.sh`
//     (see Authoritative-vs-tarball trade-off below).
//  4. Updating LatestAlias if the new tag is the newest.
//
// # Authoritative-vs-tarball SHA
//
// SHA256 is the hash of the raw .tar.gz body as served by GitHub. It's
// fast (in-line during the HTTP read via TeeReader) but NOT byte-stable
// across time: GitHub regenerates source-tarballs lazily and the gzip
// metadata can shift. If a regenerated archive yields a new digest,
// every pinned version fails until the catalogue is bumped.
//
// ContentSHA is the hash of the EXTRACTED cluster/compose/localnet/
// subtree (file paths + content, sorted), computed after extraction.
// It depends only on the files we actually keep, so it's stable across
// GitHub's archive regeneration. When set, it is the authoritative
// integrity check; SHA256 stays as the fast first-pass guard but a
// SHA256 mismatch falls through to a ContentSHA check before failing,
// so a regenerated tarball with the same extracted content still
// works.
type Version struct {
	Tag        string
	SHA256     string // hash of the raw .tar.gz body
	ContentSHA string // hash of the extracted localnet subtree (sorted, path+content)
	Size       int64
	// Major selects which Adapter implementation to use. Set per entry
	// in SupportedVersions; callers look up the adapter via the
	// AdapterFor helper in package localnet (kept outside splice to
	// avoid an import cycle with splice/v0X).
	Major string
}

// SupportedVersions is the curated list of Splice tags DevKit knows how to
// run. Keys are the user-facing version strings accepted by `--version`.
//
// SHA256 values were computed against
// `https://github.com/canton-network/splice/archive/refs/tags/<tag>.tar.gz`
// at the time this entry was added. ContentSHA values come from
// `scripts/compute-tree-sha.sh <extracted-dir>` and are authoritative.
// Tarball size is informational and used only to print a hint before
// download.
var SupportedVersions = map[string]Version{
	"0.5.18": {
		Tag:        "0.5.18",
		SHA256:     "7babdfb642513a11205baa99866b407c6233b14c30f2f2edd9a41c567bf81f67",
		ContentSHA: "1a9d0b329cd7c9f76c04085674b9e8dc98169064b0404d007d7e2b22cb582150",
		Size:       124198413,
		Major:      "0.5",
	},
	"0.6.3": {
		Tag:        "0.6.3",
		SHA256:     "286858362fc3bcd9e7771d440a43ac15a3fbbb684f166a75ae5f1a011a8bcc55",
		ContentSHA: "4d8e7f4eb73309574d3a023f9c13c7e00095ccb0741e06ac9eaf20fe12d45c17",
		Size:       137565949,
		Major:      "0.6",
	},
	"0.6.4": {
		Tag:        "0.6.4",
		SHA256:     "db725114af58aba5a4b3ff0bff2cd4552f5f5c0f57334a9b91816c1853f8f58d",
		ContentSHA: "db1e1336dc4e33abe7011a0df29e5becd141d11c84cdf42849e48bf2106066af",
		Size:       137576613,
		Major:      "0.6",
	},
}

// LatestAlias is the version returned when the user passes `--version latest`
// (or omits the flag). It MUST be a key in SupportedVersions.
const LatestAlias = "0.6.4"

// Resolve maps a user-supplied version string to a Version. The special
// value "latest" maps to LatestAlias. Anything not in SupportedVersions
// returns an error listing the supported tags.
func Resolve(req string) (Version, error) {
	if req == "" || req == "latest" {
		req = LatestAlias
	}
	v, ok := SupportedVersions[req]
	if !ok {
		return Version{}, fmt.Errorf("unsupported Splice version %q; supported: %s",
			req, strings.Join(Supported(), ", "))
	}
	return v, nil
}

// Supported returns the supported tags sorted ascending. Stable order makes
// it easy to grep error messages and renders the version list
// deterministically across hosts.
func Supported() []string {
	out := make([]string, 0, len(SupportedVersions))
	for k := range SupportedVersions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
