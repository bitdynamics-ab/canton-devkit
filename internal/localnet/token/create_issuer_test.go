package token

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunCreate_RejectsUnknownIssuer pins the guard added for PR #297
// review: on the on-ledger path, an issuer that is neither empty, a role
// name, a "::" party id, nor a registered alias (e.g. the typo "alcie")
// must be REJECTED — not silently redirected to the --role's own party,
// which would mint the token under the wrong party. Default-party
// resolution is reserved for an empty issuer or a recognized role name.
func TestRunCreate_RejectsUnknownIssuer(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	opts := happyOpts("demo")
	opts.Role = "app-user"
	opts.Endpoint = "127.0.0.1:1" // on-ledger path; the guard returns before any dial
	opts.Issuer = "alcie"         // typo for "alice" — not a role, alias, or party id

	var out bytes.Buffer
	if _, err := RunCreate(&out, opts); err == nil {
		t.Fatal("want an error for an unknown issuer, got nil")
	} else if !strings.Contains(err.Error(), "unknown issuer") {
		t.Errorf("want an 'unknown issuer' error, got: %v", err)
	}
}
