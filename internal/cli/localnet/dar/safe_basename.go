package dar

import (
	"path/filepath"
	"strings"
)

// safeBasename strips a participant-supplied string of any path
// component so it can't be used to escape the caller's CWD when
// composed into an output filename. `dar download` uses it to
// neutralise hostile `data.name` / `data.version` values from a
// compromised participant.
//
// Rules:
//   - filepath.Base reduces any directory traversal to the leaf
//     ("../../etc/passwd" → "passwd").
//   - Empty/dot/separator-only results become "" so the caller falls
//     back to the package-id default.
//   - Forward slashes, backslashes, NUL, and control chars become "_"
//     — defence in depth for separators Base() doesn't split on
//     (filepath.Base on Linux doesn't split `\`, which Windows paths
//     can mix with `/`).
//
// Length is deliberately not truncated here — that's the OS layer's
// concern.
func safeBasename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Strip leading/embedded path traversal first via filepath.Base.
	s = filepath.Base(s)
	if s == "." || s == ".." || s == string(filepath.Separator) {
		return ""
	}
	// Scrub any remaining metacharacters.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '/', r == '\\', r == 0:
			b.WriteRune('_')
		case r < 0x20:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
