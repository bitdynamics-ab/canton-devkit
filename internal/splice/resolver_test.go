package splice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMajorFromTag pins the tag → adapter-major parse since it routes
// every uncurated bring-up to the correct internal/splice/v0X
// implementation. Tagged Splice releases are "0.N.M[-suffix]" — the
// regex must match all three shapes and reject anything that isn't a
// semver-shaped prefix.
func TestMajorFromTag(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0.6.5", "0.6"},
		{"v0.6.5", "0.6"},
		{"0.7.0-alpha.4", "0.7"},
		{"v1.0.0-rc.1", "1.0"},
		{"main", ""},
		{"", ""},
		{"latest", ""},
	}
	for _, c := range cases {
		if got := majorFromTag(c.in); got != c.want {
			t.Errorf("majorFromTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestResolvedCache_RoundTrip verifies that a write followed by a
// read returns the same entries, and that LookupResolved finds them.
// Uses a temp HOME so we don't touch the real ~/.canton-devkit/cache.
func TestResolvedCache_RoundTrip(t *testing.T) {
	withTempCache(t)

	c := &ResolvedVersionCache{
		Entries: []ResolvedVersion{
			{Version: Version{Tag: "0.7.0-alpha.4", Commit: "abc123", Major: "0.7"}},
			{Version: Version{Tag: "0.7.0-alpha.5", Commit: "def456", Major: "0.7"}},
		},
	}
	if err := WriteResolvedCache(c); err != nil {
		t.Fatalf("WriteResolvedCache: %v", err)
	}
	got, err := ReadResolvedCache()
	if err != nil {
		t.Fatalf("ReadResolvedCache: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries: got %d want 2", len(got.Entries))
	}
	hit, ok := got.LookupResolved("0.7.0-alpha.4")
	if !ok || hit.Commit != "abc123" {
		t.Errorf("LookupResolved miss or wrong commit: hit=%+v ok=%v", hit, ok)
	}
}

// TestResolvedCache_MissingFile is the first-run path: no cache on
// disk → empty cache, no error. Anything else would force every
// fresh install through a network round-trip even for repeat curated
// queries (which never hit the cache anyway, but the API should be
// forgiving).
func TestResolvedCache_MissingFile(t *testing.T) {
	withTempCache(t)
	got, err := ReadResolvedCache()
	if err != nil {
		t.Fatalf("expected nil err on missing file, got %v", err)
	}
	if got == nil || len(got.Entries) != 0 {
		t.Errorf("expected empty cache, got %+v", got)
	}
}

// TestResolveUpstream_HitsCacheOnSecondCall locks in the "don't
// re-hit GitHub if we've already resolved this tag" guarantee. We
// run a fake GitHub server, count its hits, and assert the second
// ResolveUpstream call doesn't increment the counter.
func TestResolveUpstream_HitsCacheOnSecondCall(t *testing.T) {
	withTempCache(t)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		// Lightweight tag → object.type=="commit" → no second hop.
		if !strings.HasSuffix(r.URL.Path, "/0.7.0-alpha.4") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, `{"object":{"type":"commit","sha":"abcabcabc"}}`)
	}))
	defer srv.Close()

	origEndpoint := upstreamTagRefEndpoint
	upstreamTagRefEndpoint = srv.URL + "/repos/canton-network/splice/git/refs/tags/"
	defer func() { upstreamTagRefEndpoint = origEndpoint }()

	v, err := ResolveUpstream(context.Background(), "0.7.0-alpha.4")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if v.Commit != "abcabcabc" || v.Major != "0.7" || v.ContentSHA != "" {
		t.Errorf("first call returned %+v", v)
	}
	if hits != 1 {
		t.Errorf("first call hits: got %d want 1", hits)
	}

	v2, err := ResolveUpstream(context.Background(), "0.7.0-alpha.4")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if v2.Commit != "abcabcabc" {
		t.Errorf("second call commit mismatch: %+v", v2)
	}
	if hits != 1 {
		t.Errorf("second call should hit cache, but server hits incremented to %d", hits)
	}
}

// TestResolveUpstream_404PropagatesCleanly proves that a typo or
// truly-missing tag surfaces a clear error mentioning "not found"
// — not a generic decode error.
func TestResolveUpstream_404PropagatesCleanly(t *testing.T) {
	withTempCache(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	origEndpoint := upstreamTagRefEndpoint
	upstreamTagRefEndpoint = srv.URL + "/repos/canton-network/splice/git/refs/tags/"
	defer func() { upstreamTagRefEndpoint = origEndpoint }()

	_, err := ResolveUpstream(context.Background(), "0.99.99-nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to mention 'not found', got %v", err)
	}
}

// TestResolveOrUpstream_DefaultPathOnlyCurated proves the audited
// default: without allowUncurated=true, an uncurated tag is
// rejected with a hint about the --allow-uncurated opt-in, and we
// never reach the network. This is the security floor of the
// two-layer model — users who don't opt in stay in the catalogue.
func TestResolveOrUpstream_DefaultPathOnlyCurated(t *testing.T) {
	withTempCache(t)

	// Sabotage the endpoint so a stray network call would fail loudly.
	origEndpoint := upstreamTagRefEndpoint
	upstreamTagRefEndpoint = "http://127.0.0.1:1/should-never-be-hit/"
	defer func() { upstreamTagRefEndpoint = origEndpoint }()

	_, fromUpstream, err := ResolveOrUpstream(context.Background(), "0.99.0-rc1", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if fromUpstream {
		t.Error("default path must not return fromUpstream=true")
	}
	if !errors.Is(err, ErrUncuratedTag) {
		t.Errorf("expected ErrUncuratedTag, got %v", err)
	}
	if !strings.Contains(err.Error(), "--allow-uncurated") {
		t.Errorf("error should hint at --allow-uncurated, got %v", err)
	}
}

// TestResolveOrUpstream_OptInFallsThrough is the symmetric case:
// allowUncurated=true + uncurated tag → goes through ResolveUpstream
// and returns fromUpstream=true so the orchestrator can print the
// "this is uncurated" warning.
func TestResolveOrUpstream_OptInFallsThrough(t *testing.T) {
	withTempCache(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"object":{"type":"commit","sha":"fff111"}}`)
	}))
	defer srv.Close()
	origEndpoint := upstreamTagRefEndpoint
	upstreamTagRefEndpoint = srv.URL + "/repos/canton-network/splice/git/refs/tags/"
	defer func() { upstreamTagRefEndpoint = origEndpoint }()

	v, fromUpstream, err := ResolveOrUpstream(context.Background(), "0.7.0-alpha.4", true)
	if err != nil {
		t.Fatalf("ResolveOrUpstream: %v", err)
	}
	if !fromUpstream {
		t.Error("expected fromUpstream=true on uncurated opt-in path")
	}
	if v.Commit != "fff111" {
		t.Errorf("got commit %q want fff111", v.Commit)
	}
}

// TestResolveOrUpstream_CuratedTagSkipsNetwork is the fast-path
// guarantee: even with allowUncurated=true, a curated tag must not
// touch the network. Otherwise the option becomes a perf regression
// for the common case.
func TestResolveOrUpstream_CuratedTagSkipsNetwork(t *testing.T) {
	withTempCache(t)

	origEndpoint := upstreamTagRefEndpoint
	upstreamTagRefEndpoint = "http://127.0.0.1:1/should-never-be-hit/"
	defer func() { upstreamTagRefEndpoint = origEndpoint }()

	tag := LatestAlias
	v, fromUpstream, err := ResolveOrUpstream(context.Background(), tag, true)
	if err != nil {
		t.Fatalf("ResolveOrUpstream(curated): %v", err)
	}
	if fromUpstream {
		t.Error("curated tag should return fromUpstream=false")
	}
	if v.Tag != tag {
		t.Errorf("tag: got %q want %q", v.Tag, tag)
	}
}

// TestResolveForOperation_ThreeTiers pins the operability contract: an
// already-running instance must stay operable from a fresh shell across
// all three resolution tiers — curated catalogue, resolved cache, and
// bare Major-from-tag inference — where Resolve alone would reject the
// uncurated tag with ErrUncuratedTag and strand down/restart/logs/clean.
func TestResolveForOperation_ThreeTiers(t *testing.T) {
	withTempCache(t)

	// Tier 1: curated tag resolves from the catalogue.
	if v, err := ResolveForOperation(LatestAlias); err != nil || v.Tag != LatestAlias {
		t.Errorf("curated: got (%+v, %v), want tag %q", v, err, LatestAlias)
	}

	// Tier 2: an uncurated tag recorded in the resolved cache resolves
	// to the cached Version (with its Major).
	c := &ResolvedVersionCache{Entries: []ResolvedVersion{
		{Version: Version{Tag: "0.7.0-alpha.4", Commit: "abc123", Major: "0.7"}},
	}}
	if err := WriteResolvedCache(c); err != nil {
		t.Fatal(err)
	}
	v, err := ResolveForOperation("0.7.0-alpha.4")
	if err != nil || v.Major != "0.7" || v.Commit != "abc123" {
		t.Errorf("cached: got (%+v, %v), want Major 0.7 commit abc123", v, err)
	}

	// Tier 3: an uncurated tag NOT in the cache still yields a Version
	// carrying the Major inferred from the tag, so the adapter resolves.
	v, err = ResolveForOperation("0.8.1-rc.2")
	if err != nil || v.Major != "0.8" {
		t.Errorf("inferred: got (%+v, %v), want Major 0.8", v, err)
	}

	// A genuinely unparseable tag is the only error case.
	if _, err := ResolveForOperation("not-a-version"); err == nil {
		t.Error("expected error for an unparseable version tag")
	}
}

// withTempCache redirects CacheRoot to a per-test temp dir so we
// never read or write the user's real ~/.canton-devkit/cache.
func withTempCache(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// On macOS UserHomeDir consults $HOME first; this is enough.
	if got := filepath.Dir(ResolvedCachePath()); !strings.HasPrefix(got, tmp) {
		t.Fatalf("HOME override didn't take effect: cache path %q does not start with %q", got, tmp)
	}
	// Ensure no stale file in case the previous test polluted CWD-based fallback.
	_ = os.Remove(ResolvedCachePath())
}
