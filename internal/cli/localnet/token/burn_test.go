package token

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// TestConfirmBurn covers the irreversible-burn confirmation gate: an
// explicit "yes" proceeds, anything else aborts, and the prompt names
// what's about to be destroyed so the user can't confirm blind.
func TestConfirmBurn(t *testing.T) {
	opts := token.BurnOptions{Instance: "demo", Instrument: "RTK", From: "alice", Amount: "5"}

	cases := []struct {
		name   string
		input  string
		wantOK bool
	}{
		{"yes proceeds", "yes\n", true},
		{"YES case-insensitive", "YES\n", true},
		{"y alone is not enough", "y\n", false},
		{"no aborts", "no\n", false},
		{"empty aborts", "\n", false},
		{"garbage aborts", "burn it\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errw bytes.Buffer
			ok, err := confirmBurn(strings.NewReader(tc.input), &errw, opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tc.wantOK {
				t.Errorf("confirmBurn(%q) = %v, want %v", tc.input, ok, tc.wantOK)
			}
			// The prompt must surface the irreversibility + the specifics.
			prompt := errw.String()
			for _, want := range []string{"BURN", "RTK", "alice", "permanently destroys", "cannot be undone"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q; got: %s", want, prompt)
				}
			}
		})
	}
}

// A bytes.Reader is not an *os.File, so the TTY refusal branch is not
// exercised by the unit tests above (they simulate an interactive
// answer). The non-interactive refusal is driven by os.Stdin's
// char-device bit at runtime; here we just assert the reader path
// doesn't misfire the refusal for a non-*os.File reader.
func TestConfirmBurn_NonFileReaderPrompts(t *testing.T) {
	var errw bytes.Buffer
	ok, err := confirmBurn(strings.NewReader("yes\n"), &errw,
		token.BurnOptions{Instance: "i", Instrument: "X", From: "p", Amount: "1"})
	if err != nil {
		t.Fatalf("non-*os.File reader should prompt, not refuse: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true for a 'yes' answer")
	}
}
