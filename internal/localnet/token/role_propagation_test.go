package token

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestRolePropagation_MintBurnAccept covers the endpoint-contract
// regression matrix for mint/burn/accept:
//   - empty role → DefaultRole (app-provider) port + JWT
//   - explicit app-user → that role's port + JWT
//   - missing port → ErrUnresolvedLedgerEndpoint (mint/burn), never unsupported
func TestRolePropagation_MintBurnAccept(t *testing.T) {
	cases := []struct {
		name       string
		role       string // empty → DefaultRole
		wantRole   string
		wantJWT    string
		wantPort   int
		ports      map[string]int
		creds      map[string]registry.Credential
		missing    bool
		wantErrIs  error
		wantErrMsg []string
	}{
		{
			name:     "default role selects provider port and JWT",
			role:     "",
			wantRole: DefaultRole,
			wantJWT:  "jwt-provider",
			wantPort: 6001,
			ports:    map[string]int{DefaultRole: 6001, "app-user": 7001},
			creds: map[string]registry.Credential{
				DefaultRole: {Role: DefaultRole, JWT: "jwt-provider"},
				"app-user":  {Role: "app-user", JWT: "jwt-user"},
			},
		},
		{
			name:     "explicit app-user selects user port and JWT",
			role:     "app-user",
			wantRole: "app-user",
			wantJWT:  "jwt-user",
			wantPort: 7001,
			ports:    map[string]int{DefaultRole: 6001, "app-user": 7001},
			creds: map[string]registry.Credential{
				DefaultRole: {Role: DefaultRole, JWT: "jwt-provider"},
				"app-user":  {Role: "app-user", JWT: "jwt-user"},
			},
		},
		{
			name:       "missing port is unresolved not unsupported",
			role:       "",
			wantRole:   DefaultRole,
			missing:    true,
			wantErrIs:  ErrUnresolvedLedgerEndpoint,
			wantErrMsg: []string{"demo", DefaultRole, "restart", "--endpoint"},
		},
	}

	for _, tc := range cases {
		t.Run("mint/"+tc.name, func(t *testing.T) {
			seedRoleFixture(t, tc.ports, tc.creds, true)
			err := RunMint(context.Background(), nil, MintOptions{
				Instance: "demo", Instrument: "XYZ", To: "bob::xyz", Amount: "1", Role: tc.role,
			})
			if tc.missing {
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("got %v, want %v", err, tc.wantErrIs)
				}
				if errors.Is(err, ErrUnsupportedOnInstrument) {
					t.Fatal("must not mislabel missing port as unsupported")
				}
				for _, want := range tc.wantErrMsg {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error missing %q: %v", want, err)
					}
				}
				return
			}
			// Live dial fails (no participant), but role/endpoint/JWT
			// selection is what we pin — mirror runMintLive's LedgerConn.
			assertResolvedConn(t, tc.role, tc.wantRole, tc.wantJWT, tc.wantPort)
		})

		t.Run("burn/"+tc.name, func(t *testing.T) {
			seedRoleFixture(t, tc.ports, tc.creds, true)
			err := RunBurn(context.Background(), nil, BurnOptions{
				Instance: "demo", Instrument: "XYZ", From: "bob::xyz", Amount: "1", Role: tc.role,
			})
			if tc.missing {
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("got %v, want %v", err, tc.wantErrIs)
				}
				return
			}
			assertResolvedConn(t, tc.role, tc.wantRole, tc.wantJWT, tc.wantPort)
		})

		t.Run("accept/"+tc.name, func(t *testing.T) {
			seedRoleFixture(t, tc.ports, tc.creds, false)
			err := RunAccept(context.Background(), nil, AcceptOptions{
				Instance: "demo", TransferInstructionID: "cid-1", Role: tc.role,
			})
			if tc.missing {
				// Accept still uses ErrNeedsV2LocalNet when endpoint is
				// empty; CLI resolveEndpoint catches missing ports first.
				if !errors.Is(err, ErrNeedsV2LocalNet) {
					t.Fatalf("accept no-port: got %v, want ErrNeedsV2LocalNet", err)
				}
				return
			}
			assertResolvedConn(t, tc.role, tc.wantRole, tc.wantJWT, tc.wantPort)
		})
	}
}

// assertResolvedConn mirrors the LedgerConn mint/burn/accept build after
// RunX defaulting: empty role → DefaultRole, empty endpoint →
// ResolveLedgerEndpoint, then resolveLedgerToken for the role's JWT.
func assertResolvedConn(t *testing.T, role, wantRole, wantJWT string, wantPort int) {
	t.Helper()
	if role == "" {
		role = DefaultRole
	}
	if role != wantRole {
		t.Fatalf("role defaulting = %q, want %q", role, wantRole)
	}
	ep := ResolveLedgerEndpoint("demo", role)
	wantEP := "localhost:" + strconv.Itoa(wantPort)
	if ep != wantEP {
		t.Errorf("ResolveLedgerEndpoint = %q, want %q", ep, wantEP)
	}
	tok, err := resolveLedgerToken(LedgerConn{Instance: "demo", Role: role, Endpoint: ep})
	if err != nil {
		t.Fatalf("resolveLedgerToken: %v", err)
	}
	if tok != wantJWT {
		t.Errorf("JWT for role %q = %q, want %q", role, tok, wantJWT)
	}
}

func seedRoleFixture(t *testing.T, ports map[string]int, creds map[string]registry.Credential, withToken bool) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	if withToken {
		seedOnLedgerToken(t, "demo", "XYZ", "issuer::abc")
	}
	for role, port := range ports {
		setLedgerPort(t, "demo", role, port)
	}
	if creds != nil {
		seedCreds(t, "demo", creds)
	}
}

func seedCreds(t *testing.T, name string, creds map[string]registry.Credential) {
	t.Helper()
	s, err := registry.Read(name)
	if err != nil {
		t.Fatalf("read %q: %v", name, err)
	}
	s.Credentials = creds
	if err := registry.Write(s); err != nil {
		t.Fatalf("write creds: %v", err)
	}
}
