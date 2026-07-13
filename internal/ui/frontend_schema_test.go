package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// TestFrontend_SchemaVersionMatchesTypes is the cross-language parity
// pin: the SCHEMA_VERSION constant in frontend/src/api.ts MUST equal
// types.SchemaVersion, otherwise the frontend's bootstrap handshake
// refuses to render — but only at runtime. This test moves that
// failure to CI.
//
// Detection: regex-grep the source for `SCHEMA_VERSION = N`. The
// declaration is a stable one-line pattern; a JS parser dep just to
// read one integer would be over-engineering.
func TestFrontend_SchemaVersionMatchesTypes(t *testing.T) {
	// Resolve relative to the package dir so the test runs
	// from `go test ./internal/ui/...` regardless of cwd.
	src, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "api.ts"))
	if err != nil {
		// Frontend dir may legitimately be absent in some build
		// modes (a future Go-only release variant). Skip rather
		// than fail; production CI runs full `make frontend`
		// before this and the file will be present.
		t.Skipf("frontend/src/api.ts not readable, skipping (cwd-relative path): %v", err)
	}

	re := regexp.MustCompile(`(?m)^export const SCHEMA_VERSION\s*=\s*(\d+)`)
	m := re.FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("frontend/src/api.ts: SCHEMA_VERSION declaration not found — regex needs updating?")
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("SCHEMA_VERSION value %q not an int: %v", m[1], err)
	}
	if got != types.SchemaVersion {
		t.Errorf("frontend SCHEMA_VERSION = %d, types.SchemaVersion = %d — bootstrap handshake will reject every fetch", got, types.SchemaVersion)
	}
}

// TestFrontend_DistContainsRealBuildOrPlaceholder is a sanity check:
// dist/index.html must either carry the placeholder sentinel (dev /
// not-yet-built state) OR be the real Vite bundle (post-`make
// frontend`). A file that's neither suggests something else got
// written to the embed location.
func TestFrontend_DistContainsRealBuildOrPlaceholder(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("dist", "index.html"))
	if err != nil {
		t.Fatalf("dist/index.html: %v — should always be present so go:embed has a target", err)
	}
	hasPlaceholder := strings.Contains(string(body), placeholderSentinel)
	// A real Vite build pulls in a /assets/index-*.js script tag.
	hasViteScript := strings.Contains(string(body), `type="module"`) &&
		(strings.Contains(string(body), "/assets/index-") ||
			strings.Contains(string(body), "/src/main.tsx"))
	if !hasPlaceholder && !hasViteScript {
		t.Errorf("dist/index.html is neither the placeholder nor a Vite build — what wrote this?\n%s",
			string(body[:min(200, len(body))]))
	}
}

// TestFrontend_TrackedDistIsPlaceholder pins the git-tracked content of
// dist/index.html to the placeholder. The working-tree file is
// legitimately overwritten by `make frontend`, so this checks what's
// committed (git show HEAD:...) rather than the file on disk.
//
// Why this matters: a real Vite build's index.html references hashed
// /assets/index-*.js and *.css files that are git-ignored. Committing
// such a build over the placeholder means a fresh checkout points at
// files that were never committed. Keep the placeholder tracked; let
// `make frontend` produce the real bundle at build time.
func TestFrontend_TrackedDistIsPlaceholder(t *testing.T) {
	// Package dir is internal/ui; repo root is two levels up.
	cmd := exec.Command("git", "show", "HEAD:internal/ui/dist/index.html")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		// No git (source tarball) or path not tracked: nothing to
		// assert about committed content. Skip, consistent with the
		// other frontend tests that tolerate a missing source tree.
		t.Skipf("cannot read git-tracked dist/index.html, skipping: %v", err)
	}
	if !strings.Contains(string(out), placeholderSentinel) {
		t.Errorf("git-tracked internal/ui/dist/index.html is not the placeholder "+
			"(missing %q). A real Vite build was likely committed over it; the "+
			"hashed /assets/*.js and *.css it references are git-ignored, so a "+
			"fresh checkout breaks. Restore the placeholder version.\n%s",
			placeholderSentinel, string(out[:min(200, len(out))]))
	}
}
