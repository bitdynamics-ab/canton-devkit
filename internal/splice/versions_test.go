package splice

import (
	"strings"
	"testing"
)

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
	if !strings.Contains(err.Error(), "supported:") {
		t.Errorf("error should list supported tags, got %v", err)
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
