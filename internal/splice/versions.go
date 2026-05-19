package splice

import (
	"fmt"
	"sort"
	"strings"
)

// Version describes a curated, tested Splice release. DevKit only allows
// `localnet up` against versions in this map so users never compose-up an
// untested upstream tag. Adding a new entry requires:
//
//  1. Downloading the upstream tag's source tarball from
//     https://github.com/canton-network/splice/archive/refs/tags/<tag>.tar.gz
//  2. Running `shasum -a 256` and recording the digest below.
//  3. Updating LatestAlias if the new tag is the newest.
type Version struct {
	Tag    string
	SHA256 string
	Size   int64
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
// at the time this entry was added. Tarball size is informational and used
// only to print a hint before download.
var SupportedVersions = map[string]Version{
	"0.5.18": {
		Tag:    "0.5.18",
		SHA256: "7babdfb642513a11205baa99866b407c6233b14c30f2f2edd9a41c567bf81f67",
		Size:   124198413,
		Major:  "0.5",
	},
	"0.6.3": {
		Tag:    "0.6.3",
		SHA256: "286858362fc3bcd9e7771d440a43ac15a3fbbb684f166a75ae5f1a011a8bcc55",
		Size:   137565949,
		Major:  "0.6",
	},
	"0.6.4": {
		Tag:    "0.6.4",
		SHA256: "db725114af58aba5a4b3ff0bff2cd4552f5f5c0f57334a9b91816c1853f8f58d",
		Size:   137576613,
		Major:  "0.6",
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
