package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
)

// TestFrontend_SchemaVersionMatchesTypes is the cross-language
// parity pin // the SCHEMA_VERSION constant in frontend/src/api.ts MUST equal
// types.SchemaVersion. Without this, the frontend's bootstrap
// handshake reports a v1 client against a v2 server (or vice
// versa) and the App refuses to render — but only at runtime,
// after the user has a confused experience. The lint moves that
// failure to CI.
//
// Detection: regex-grep the source file for `SCHEMA_VERSION = N`.
// We don't run a JS parser here; the constant declaration is a
// stable one-line pattern, and adding a real parser dep just to
// read one integer is over-engineering.
//
// Catch class: someone bumps types.SchemaVersion without
// updating the frontend constant, or vice versa.
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

// TestFrontend_DistContainsRealBuildOrPlaceholder pins the invariant —
// release-mode sanity check: at test time, dist/index.html must
// either carry the placeholder sentinel (dev / not-yet-built
// state) OR be the real Vite bundle (post-`make frontend`). A
// file that's neither suggests something else got written to the
// embed location.
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
