package splice

import (
	"errors"
	"strings"
	"testing"
)

// TestAlphaChannelEntryShape pins that the catalogue carries at least
// one alpha entry with a non-empty ImageRepo override — the Token
// Standard V2 snapshot — and that the IsAlpha helper agrees. This is
// Contract: alpha catalogue entries opt into a separate image repo
// via the new fields, and the CLI/up paths key off
// `Channel == "alpha"`.
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

// TestVersionsJSONIsValid sanity-checks the embedded catalogue: every
// commit SHA looks like a hex git SHA (40 chars), every ContentSHA is
// SHA-256 hex (64 chars). Catches typos that init() would also catch
// but with a clearer error.
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
	// Resolve returns ErrUncuratedTag (rather than a generic
	// "unsupported" error) so orchestrators can distinguish
	// "tag exists nowhere" from "tag is upstream but not curated."
	// The list of curated tags is still embedded so users see what
	// the safe options are.
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

func TestSupportedIsSorted(t *testing.T) {
	got := Supported()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("Supported() not sorted: %v", got)
			break
		}
	}
}
