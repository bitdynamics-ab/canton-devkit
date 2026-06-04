package cli

import "testing"

type pathCmd struct{ path string }

func (p pathCmd) CommandPath() string { return p.path }

// TestTokenAction pins the bucket extraction for dpm/token_action: the
// direct child of `localnet token`, derived from the cobra path (never
// raw args, so no instrument name / flag leaks). Covers both binary names
// (dpm component + standalone) and the nested `party` group.
func TestTokenAction(t *testing.T) {
	cases := map[string]string{
		"canton-devkit localnet token mint":      "mint",
		"dpm localnet token create":              "create",
		"canton-devkit localnet token transfer":  "transfer",
		"dpm localnet token transfer accept":     "transfer", // accept is under transfer
		"canton-devkit localnet token party new": "party",
		"canton-devkit localnet token balance":   "balance",
		"canton-devkit localnet token":           "", // bare parent — no action
		"canton-devkit localnet up":              "", // not a token command
		"":                                       "",
	}
	for path, want := range cases {
		if got := tokenAction(pathCmd{path}); got != want {
			t.Errorf("tokenAction(%q) = %q, want %q", path, got, want)
		}
	}
}
