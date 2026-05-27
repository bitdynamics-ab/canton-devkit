package handlers

import "testing"

// TestFirstNonWarningLine_SkipsWarnings pins BIT-176: the user
// saw "Stop failed: Warning: could not reconstruct compose
// context..." when the actual fatal cause was on a later line.
// This test locks the warning-skip behavior so a future change to
// firstNonWarningLine can't silently reintroduce that bug.
func TestFirstNonWarningLine_SkipsWarnings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "BIT-176 reproduction — Warning before fatal cause",
			in: "Warning: could not reconstruct compose context: resolve version \"0.4.12\" from state: tag is not in the curated DevKit catalogue\n" +
				"docker compose down failed: project canton-demo has no compose file\n",
			want: `docker compose down failed: project canton-demo has no compose file`,
		},
		{
			name: "all-warnings fallthrough — surface first raw line",
			in:   "Warning: could not scrub orphan registry entry: not found\nWarning: could not persist failed state: disk full\n",
			want: "Warning: could not scrub orphan registry entry: not found",
		},
		{
			name: "no warnings — first line wins",
			in:   "Failed to read state: permission denied\n",
			want: "Failed to read state: permission denied",
		},
		{
			name: "case-insensitive — lower/upper/mixed prefixes",
			in: "warning: lower-case skipped\n" +
				"WARN: short form skipped\n" +
				"the actual cause\n",
			want: "the actual cause",
		},
		{
			name: "indented warning still skipped (defensive trim)",
			in:   "  Warning: indented\nactual cause here\n",
			want: "actual cause here",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "single line, no newline",
			in:   "single line cause",
			want: "single line cause",
		},
		{
			// BIT-176 review fix: docker-compose on Windows
			// emits CRLF line endings; the \r must NOT defeat
			// the prefix match.
			name: "CRLF line endings — \\r before \\n must not break the prefix match",
			in:   "Warning: stale state\r\nthe real cause line\r\n",
			want: "the real cause line",
		},
		{
			// Covers the WARN: arm that the original test set
			// missed.
			name: "WARN: short form — case variants all match",
			in:   "WARN: short form\nwarn: lower form\nWarn: title form\nthe real cause\n",
			want: "the real cause",
		},
		{
			name: "WARNING: ALL-CAPS variant",
			in:   "WARNING: caps\nthe real cause\n",
			want: "the real cause",
		},
		{
			// Edge: warning prefix that's only 7 chars (no colon)
			// must NOT match — protects against eating "Warning"
			// as a substring of a legitimate cause.
			name: "no colon after Warning — must not match",
			in:   "Warning is the way\nactual cause\n",
			want: "Warning is the way",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstNonWarningLine(c.in)
			if got != c.want {
				t.Errorf("firstNonWarningLine(%q)\n  got:  %q\n  want: %q",
					c.in, got, c.want)
			}
		})
	}
}
