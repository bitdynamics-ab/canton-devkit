package dar

import (
	"path/filepath"
	"strings"
)

// safeBasename strips a participant-supplied string of any path
// component so it can't be used to escape the caller's CWD when
// composed into an output filename. Used by `dar download` (BIT-127
// review fix) to neutralise hostile `data.name` / `data.version`
// values from a compromised participant.
//
// Rules:
//   - filepath.Base reduces any directory traversal to the leaf
//     ("../../etc/passwd" → "passwd").
//   - Empty/dot/separator-only results become "" so the caller
//     falls back to the package-id default.
//   - Forward slashes, backslashes, NUL, and control chars are
//     replaced with "_" — defence in depth in case Base() lets
//     something through on a non-Unix path (Windows paths can
//     mix `\` and `/` and filepath.Base on Linux doesn't split
//     on `\`).
//
// We deliberately don't truncate length here — that's a separate
// concern handled at the OS layer. We just refuse to be the path-
// composer for a participant-controlled string.
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
	// Now scrub any remaining metacharacters. We rebuild via a
	// strings.Builder to avoid mutating allocs on the happy path.
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
