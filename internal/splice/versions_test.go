package splice

import (
	"errors"
	"strings"
	"testing"
)

func TestAlphaChannelEntryShape(t *testing.T) {
	var sawAlpha bool
	for tag, v := range SupportedVersions {
		if !v.IsAlpha() {
			if v.Channel != "" && v.Channel != "stable" {
				t.Errorf("%s: unexpected Channel %q (only \"\" / \"stable\" / \"alpha\" are valid)", tag, v.Channel)
			}
			continue
		}
		sawAlpha = true
		if v.ImageRepo == "" {
			t.Errorf("%s: alpha entry must set ImageRepo (otherwise IMAGE_REPO override is a no-op and the user pulls stable images on an alpha branch)", tag)
		}
		if v.ContentSHA == "" || v.Commit == "" {
			t.Errorf("%s: alpha entry still requires Commit + ContentSHA for integrity verification", tag)
		}
	}
	if !sawAlpha {
		t.Skip("no alpha-channel entries in the catalogue (acceptable if V2 has been promoted to stable)")
	}
}

func TestTokenStandardV2CatalogueEntry(t *testing.T) {
	v, err := Resolve("token-standard-v2")
	if err != nil {
		t.Fatalf("Resolve(token-standard-v2): %v", err)
	}
	if v.Commit != "de911e38af78ec79b6f0e6a9515e104b1e4c3d62" {
		t.Errorf("Commit = %q", v.Commit)
	}
	if v.ContentSHA != "4fae533b28de046360524c0e4b9d56a5799865bfff6007b5372959bf150165c7" {
		t.Errorf("ContentSHA = %q", v.ContentSHA)
	}
	if v.Size != 154042565 {
		t.Errorf("Size = %d", v.Size)
	}
	if v.Major != "0.6" {
		t.Errorf("Major = %q", v.Major)
	}
	if v.Channel != "alpha" {
		t.Errorf("Channel = %q", v.Channel)
	}
	if !v.IsAlpha() {
		t.Error("IsAlpha() = false, want true")
	}
	if v.ImageRepo != "ghcr.io/digital-asset/decentralized-canton-sync-dev/docker" {
		t.Errorf("ImageRepo = %q", v.ImageRepo)
	}
}

func TestSupportsTokenStandardV2(t *testing.T) {
	tests := []struct {
		tag  string
		want bool
	}{
		{"0.5.18", false},
		{"0.6.10", false},
		{"0.6.11", true},
		{"0.6.12", true},
		{"0.7.0", true},
		{"0.8.0", true},
		{"0.8.1", true},
		{"1.0.0", true},
		{"token-standard-v2", true},
		{"latest", false},
		{"invalid", false},
	}
	for _, tt := range tests {
		if got := SupportsTokenStandardV2(tt.tag); got != tt.want {
			t.Errorf("SupportsTokenStandardV2(%q) = %v, want %v", tt.tag, got, tt.want)
		}
	}
}

func TestResolveSupportedTag(t *testing.T) {
	for tag := range SupportedVersions {
		v, err := Resolve(tag)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tag, err)
			continue
		}
		if v.Tag != tag {
			t.Errorf("Resolve(%q) returned Tag=%q", tag, v.Tag)
		}
		if v.Commit == "" {
			t.Errorf("Resolve(%q) has empty Commit — every curated entry must be pinned to an immutable commit SHA", tag)
		}
		if v.ContentSHA == "" {
			t.Errorf("Resolve(%q) has empty ContentSHA — every curated entry must be content-hashed", tag)
		}
		if v.Major == "" {
			t.Errorf("Resolve(%q) has empty Major — needed for adapter routing", tag)
		}
	}
}

func TestVersionsJSONIsValid(t *testing.T) {
	for tag, v := range SupportedVersions {
		if len(v.Commit) != 40 {
			t.Errorf("%q: commit %q is not a 40-char git SHA", tag, v.Commit)
		}
		if len(v.ContentSHA) != 64 {
			t.Errorf("%q: content_sha %q is not a 64-char SHA-256 hex", tag, v.ContentSHA)
		}
	}
}

func TestResolveLatestAndEmpty(t *testing.T) {
	for _, in := range []string{"latest", ""} {
		v, err := Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if v.Tag != LatestAlias {
			t.Errorf("Resolve(%q) Tag = %q, want %q", in, v.Tag, LatestAlias)
		}
	}
}

func TestResolveUnsupported(t *testing.T) {
	_, err := Resolve("9.99.99")
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
	if !errors.Is(err, ErrUncuratedTag) {
		t.Errorf("expected ErrUncuratedTag, got %v", err)
	}
	if !strings.Contains(err.Error(), "curated tags:") {
		t.Errorf("error should list curated tags, got %v", err)
	}
}

func TestLatestAliasIsInSupported(t *testing.T) {
	if _, ok := SupportedVersions[LatestAlias]; !ok {
		t.Fatalf("LatestAlias %q is not in SupportedVersions — would break --version latest", LatestAlias)
	}
}

func TestMinMemoryFor_UncuratedInheritsMajorFloor(t *testing.T) {
	var want06 uint64
	for _, v := range SupportedVersions {
		if v.Major == "0.6" && v.MinMemoryBytes > want06 {
			want06 = v.MinMemoryBytes
		}
	}
	if want06 == 0 {
		t.Skip("no 0.6 catalogue entry with a memory override — nothing to inherit")
	}

	uncurated := Version{Tag: "0.6.99-rc.1", Commit: "deadbeef", Major: "0.6"}
	if got := MinMemoryFor(uncurated); got != want06 {
		t.Errorf("MinMemoryFor(uncurated 0.6) = %d, want same-major override %d", got, want06)
	}

	var wantRec06 uint64
	for _, v := range SupportedVersions {
		if v.Major == "0.6" && v.RecommendedMemoryBytes > wantRec06 {
			wantRec06 = v.RecommendedMemoryBytes
		}
	}
	if got := RecommendedMemoryFor(uncurated); got != wantRec06 {
		t.Errorf("RecommendedMemoryFor(uncurated 0.6) = %d, want %d", got, wantRec06)
	}
}

func TestMinMemoryFor_UnknownMajorFallsBackToDefault(t *testing.T) {
	for _, v := range []Version{
		{Tag: "9.9.9", Major: "9.9"},
		{Tag: "weird", Major: ""},
		{},
	} {
		if got := MinMemoryFor(v); got != defaultMinMemoryBytesFallback {
			t.Errorf("MinMemoryFor(%+v) = %d, want default %d", v, got, defaultMinMemoryBytesFallback)
		}
		if got := RecommendedMemoryFor(v); got != defaultRecommendedMemoryBytesFallback {
			t.Errorf("RecommendedMemoryFor(%+v) = %d, want default %d", v, got, defaultRecommendedMemoryBytesFallback)
		}
	}
}

func TestMinMemoryFor_ExplicitOverrideWins(t *testing.T) {
	v := Version{Tag: "custom", Major: "0.6", MinMemoryBytes: 16_000_000_000, RecommendedMemoryBytes: 20_000_000_000}
	if got := MinMemoryFor(v); got != 16_000_000_000 {
		t.Errorf("MinMemoryFor explicit = %d, want 16e9", got)
	}
	if got := RecommendedMemoryFor(v); got != 20_000_000_000 {
		t.Errorf("RecommendedMemoryFor explicit = %d, want 20e9", got)
	}
}

func TestRecommendedMemoryFor_NeverBelowMin(t *testing.T) {
	v := Version{Tag: "custom", Major: "orphan-major", MinMemoryBytes: 10_000_000_000}
	if got := RecommendedMemoryFor(v); got != 10_000_000_000 {
		t.Errorf("RecommendedMemoryFor(min-only) = %d, want raised to MinMemoryFor %d", got, 10_000_000_000)
	}
}

func TestSupportedIsSorted(t *testing.T) {
	got := Supported()
	for i := 1; i < len(got); i++ {
		if compareTags(got[i-1], got[i]) > 0 {
			t.Errorf("Supported() not in version order: %v", got)
			break
		}
	}
}

func TestSupportedOrderIsNumeric(t *testing.T) {
	got := Supported()
	pos := map[string]int{}
	for i, tag := range got {
		pos[tag] = i
	}
	for _, pair := range [][2]string{{"0.6.9", "0.6.10"}, {"0.6.10", "0.6.11"}, {"0.6.4", "0.6.9"}} {
		lo, okLo := pos[pair[0]]
		hi, okHi := pos[pair[1]]
		if okLo && okHi && lo > hi {
			t.Errorf("Supported() ordered %q after %q: %v", pair[0], pair[1], got)
		}
	}
	if p, ok := pos["token-standard-v2"]; ok && p != len(got)-1 {
		t.Errorf("token-standard-v2 should sort last, got position %d in %v", p, got)
	}
}

func TestCompareTags(t *testing.T) {
	cases := []struct {
		a, b     string
		wantSign int
	}{
		{"0.6.9", "0.6.10", -1},
		{"0.6.10", "0.6.9", 1},
		{"0.6.4", "0.6.4", 0},
		{"0.5.18", "0.6.3", -1},
		{"0.6.11", "token-standard-v2", -1},
		{"token-standard-v2", "0.6.3", 1},
	}
	for _, c := range cases {
		got := compareTags(c.a, c.b)
		if (got < 0) != (c.wantSign < 0) || (got > 0) != (c.wantSign > 0) {
			t.Errorf("compareTags(%q, %q) = %d, want sign %d", c.a, c.b, got, c.wantSign)
		}
	}
}
