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
		if v.SHA256 == "" {
			t.Errorf("Resolve(%q) has empty SHA256 — every curated entry must be pinned", tag)
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
