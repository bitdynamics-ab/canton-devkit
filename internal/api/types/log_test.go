package types

import (
	"testing"
	"time"
)

// TestLogLine_ParsedTime covers the three-cell decision table:
// empty input / valid RFC3339Nano / malformed.
func TestLogLine_ParsedTime(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantOK  bool
		wantStr string // formatted RFC3339Nano of the expected parsed value; "" when wantOK=false
	}{
		{"empty", "", false, ""},
		{
			"valid RFC3339Nano",
			"2026-05-25T14:30:45.123456789Z",
			true,
			"2026-05-25T14:30:45.123456789Z",
		},
		{
			"valid RFC3339 (no nanos) — still parses",
			"2026-05-25T14:30:45Z",
			true,
			"2026-05-25T14:30:45Z",
		},
		{"malformed", "yesterday at lunch", false, ""},
		{"only date", "2026-05-25", false, ""},
		{"timezone garbage", "2026-05-25T14:30:45+NOPE", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := LogLine{Time: c.in}
			got, ok := line.ParsedTime()
			if ok != c.wantOK {
				t.Errorf("ParsedTime ok = %v, want %v (input=%q got=%v)",
					ok, c.wantOK, c.in, got)
			}
			if c.wantOK {
				if want, _ := time.Parse(time.RFC3339Nano, c.wantStr); !got.Equal(want) {
					t.Errorf("parsed = %v, want %v (input=%q)", got, want, c.in)
				}
			} else if !got.IsZero() {
				t.Errorf("parsed = %v, want zero time on failure (input=%q)", got, c.in)
			}
		})
	}
}
