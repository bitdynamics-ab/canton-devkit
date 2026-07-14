package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// TestTokenIdentity_ListsRolesAndEchoesCurrent covers the act-as
// identity endpoint that backs the Web UI role switcher: it returns the
// full role set (token.Roles(), shared with the CLI) and echoes the role
// the request was made under via ?role=, defaulting to app-user. Table-
// driven over the default + each explicit role.
func TestTokenIdentity_ListsRolesAndEchoesCurrent(t *testing.T) {
	seedForTokens(t, "demo")
	srv := tokensSrv(t)

	cases := []struct {
		name        string
		query       string
		wantCurrent string
	}{
		{"default role", "", "app-user"},
		{"explicit app-user", "&role=app-user", "app-user"},
		{"explicit app-provider", "&role=app-provider", "app-provider"},
		{"explicit sv", "&role=sv", "sv"},
	}
	wantRoles := token.Roles()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/api/tokens/identity?instance=demo" + tc.query)
			if err != nil {
				t.Fatalf("GET identity: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			var got struct {
				SchemaVersion  int      `json:"schema_version"`
				Instance       string   `json:"instance"`
				AvailableRoles []string `json:"available_roles"`
				CurrentRole    string   `json:"current_role"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			_ = resp.Body.Close()

			if got.SchemaVersion == 0 {
				t.Errorf("schema_version not set")
			}
			if got.Instance != "demo" {
				t.Errorf("instance = %q, want demo", got.Instance)
			}
			if got.CurrentRole != tc.wantCurrent {
				t.Errorf("current_role = %q, want %q", got.CurrentRole, tc.wantCurrent)
			}
			if len(got.AvailableRoles) != len(wantRoles) {
				t.Fatalf("available_roles = %v, want %v", got.AvailableRoles, wantRoles)
			}
			for i, r := range wantRoles {
				if got.AvailableRoles[i] != r {
					t.Errorf("available_roles[%d] = %q, want %q (order matters)", i, got.AvailableRoles[i], r)
				}
			}
			// The default (app-user) must lead so the switcher highlights
			// the same identity roleFromQuery falls back to.
			if got.AvailableRoles[0] != "app-user" {
				t.Errorf("available_roles must lead with app-user; got %q", got.AvailableRoles[0])
			}
		})
	}
}

// TestTokenIdentity_MissingInstanceIs400 covers the shared
// instanceFromQuery guard on the identity endpoint.
func TestTokenIdentity_MissingInstanceIs400(t *testing.T) {
	srv := tokensSrv(t)
	resp, err := http.Get(srv.URL + "/api/tokens/identity")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestTokenReadPaths_ThreadRole pins that the read endpoints powering the
// Web UI switcher forward ?role= to the orchestration layer's endpoint
// resolution — so selecting a non-default identity dials that role's
// participant, not app-user's. We assert via liveLedgerEndpoint, the seam
// every read handler resolves its endpoint through: it must be called
// with the request's role.
func TestTokenReadPaths_ThreadRole(t *testing.T) {
	seedForTokens(t, "demo") // pins liveLedgerEndpoint to a stub; we override it below

	var gotRole string
	prev := liveLedgerEndpoint
	liveLedgerEndpoint = func(_, role string) string {
		gotRole = role
		return "" // stay offline: exercise role threading without a live dial
	}
	t.Cleanup(func() { liveLedgerEndpoint = prev })

	srv := tokensSrv(t)

	// Each entry is a read endpoint that resolves its endpoint via the
	// liveLedgerEndpoint seam. A ?role=app-provider request must reach the
	// resolver as app-provider (not the app-user default) — proof the
	// switcher's role selection dials the chosen identity's participant.
	// (handleTokenHoldings resolves via token.ResolveLedgerEndpoint
	// directly, not this seam; its role threading is covered by
	// TestTokens_HoldingsRegistryFallbackTagged.)
	paths := []string{
		"/api/tokens?instance=demo&role=app-provider",
		"/api/tokens/matrix?instance=demo&role=app-provider",
		"/api/tokens/RTK/summary?instance=demo&role=app-provider",
		"/api/tokens/RTK/activity?instance=demo&role=app-provider",
	}
	for _, p := range paths {
		gotRole = ""
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		_ = resp.Body.Close()
		if gotRole != "app-provider" {
			t.Errorf("%s: role threaded to endpoint resolver = %q, want app-provider", p, gotRole)
		}
	}
}
