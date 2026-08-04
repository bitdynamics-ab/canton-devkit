package token

import (
	"bytes"
	"context"
	"strings"
	"testing"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
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

func TestResolveCreateIssuer_DefaultAndMatchingRole(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	fake := &fakeLedger{ListKnownPartiesFn: func(context.Context) (*adminv2.ListKnownPartiesResponse, error) {
		return &adminv2.ListKnownPartiesResponse{PartyDetails: []*adminv2.PartyDetails{
			{Party: "app_user_zed::1220", IsLocal: true},
			{Party: "app_user_alice::1220", IsLocal: true},
		}}, nil
	}}
	withFakeDial(t, fake)

	for _, issuer := range []string{"", "app-user"} {
		opts := happyOpts("demo")
		opts.Endpoint = "localhost:7501"
		opts.Role = "app-user"
		opts.Issuer = issuer
		got, err := resolveCreateIssuer(opts)
		if err != nil {
			t.Fatalf("resolve issuer %q: %v", issuer, err)
		}
		if got != "app_user_alice::1220" {
			t.Errorf("resolve issuer %q = %q, want alphabetically first local party", issuer, got)
		}
	}
}

func TestResolveCreateIssuer_RejectsRoleMismatch(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	opts := happyOpts("demo")
	opts.Endpoint = "localhost:7501"
	opts.Role = "app-user"
	opts.Issuer = "app-provider"

	_, err := resolveCreateIssuer(opts)
	if err == nil {
		t.Fatal("want an error for an issuer role that differs from --role")
	}
	if !strings.Contains(err.Error(), "must match --role") || !strings.Contains(err.Error(), "--role app-provider") {
		t.Errorf("want actionable role-mismatch error, got: %v", err)
	}
}
