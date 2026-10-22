package token

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestDARUnavailableError_NamesTheLaunchCommand(t *testing.T) {
	for _, tc := range []struct {
		prefix  string
		want    []string
		notWant string
	}{
		{"", []string{
			"`dpm localnet up <name> --version 0.6.12`",
			"`dpm localnet dar upload <dar-path> --instance demo --all-participants`",
		}, "canton-devkit localnet"},
		{"canton-devkit localnet", []string{
			"`canton-devkit localnet up <name> --version 0.6.12`",
			"`canton-devkit localnet dar upload <dar-path> --instance demo --all-participants`",
		}, "dpm localnet"},
	} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			if tc.prefix != "" {
				t.Cleanup(cliprefix.Set(tc.prefix))
			}
			msg := darUnavailableError("splice-test-token-v2", "0.6.4", "demo").Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message missing %q: %s", w, msg)
				}
			}
			if strings.Contains(msg, tc.notWant) {
				t.Errorf("message must not mention %q: %s", tc.notWant, msg)
			}
		})
	}
}

func TestResolveLedgerToken_CredsHintNamesTheLaunchCommand(t *testing.T) {
	for _, tc := range []struct {
		prefix  string
		want    string
		notWant string
	}{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		for _, path := range []struct {
			name     string
			role     string
			envFiles bool
			marker   string
		}{
			{"no-env-files", "app-user", false, "could not load env files"},
			{"role-not-in-env", "auditor", true, "no env entry for role"},
		} {
			t.Run("prefix="+tc.prefix+"/"+path.name, func(t *testing.T) {
				t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
				if tc.prefix != "" {
					t.Cleanup(cliprefix.Set(tc.prefix))
				}
				seedInstance(t, "demo")
				if path.envFiles {
					writeAuthEnvFiles(t, "demo")
				}
				_, err := resolveLedgerToken(LedgerConn{Instance: "demo", Role: path.role})
				if err == nil {
					t.Fatal("resolveLedgerToken must fail without credentials")
				}
				if !strings.Contains(err.Error(), path.marker) {
					t.Fatalf("error took the wrong path, want %q: %v", path.marker, err)
				}
				want := fmt.Sprintf("`%s creds demo --role %s --format raw`", tc.want, path.role)
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
				if strings.Contains(err.Error(), tc.notWant) {
					t.Errorf("error must not mention %q: %v", tc.notWant, err)
				}
			})
		}
	}
}

func writeAuthEnvFiles(t *testing.T, name string) {
	t.Helper()
	s, err := registry.Read(name)
	if err != nil {
		t.Fatalf("read %q: %v", name, err)
	}
	envDir := filepath.Join(s.ProjectDir, "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		"sv-auth-on.env":           "AUTH_SV_AUDIENCE=https://example.com\nAUTH_SV_VALIDATOR_USER_NAME=sv-user\n",
		"app-provider-auth-on.env": "AUTH_APP_PROVIDER_AUDIENCE=https://example.com\nAUTH_APP_PROVIDER_VALIDATOR_USER_NAME=app-provider-user\n",
		"app-user-auth-on.env":     "AUTH_APP_USER_AUDIENCE=https://example.com\nAUTH_APP_USER_VALIDATOR_USER_NAME=app-user-user\n",
	} {
		if err := os.WriteFile(filepath.Join(envDir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTokenHints_FollowCLIPrefix(t *testing.T) {
	for _, tc := range []struct{ set, prefix, other string }{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			if tc.set != "" {
				t.Cleanup(cliprefix.Set(tc.set))
			}
			check := func(label, got string, want ...string) {
				t.Helper()
				for _, w := range want {
					if !strings.Contains(got, w) {
						t.Errorf("%s: missing %q in %q", label, w, got)
					}
				}
				if strings.Contains(got, tc.other) {
					t.Errorf("%s: must not mention %q: %q", label, tc.other, got)
				}
				if strings.Contains(got, "`localnet ") || strings.Contains(got, "`token ") {
					t.Errorf("%s: prefix-less hint in %q", label, got)
				}
			}
			t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
			upV2 := "`" + tc.prefix + " up <name> --version 0.6.12`"
			manualDAR := "`" + tc.prefix + " dar upload <dar-path> --instance demo --all-participants`"

			_, err := RunDemo(context.Background(), nil, DemoOptions{Instance: "demo"})
			if !errors.Is(err, ErrNeedsV2LocalNet) {
				t.Fatalf("demo without an endpoint: want ErrNeedsV2LocalNet, got %v", err)
			}
			check("needs V2", err.Error(), "bring one up with "+upV2+", then re-run")
			if wrapped := fmt.Errorf("transfer: %w", ErrNeedsV2LocalNet); !errors.Is(wrapped, ErrNeedsV2LocalNet) {
				t.Errorf("wrapped ErrNeedsV2LocalNet must still match errors.Is")
			}

			seedInstance(t, "demo")
			seedOnLedgerToken(t, "demo", "XYZ", "issuer::abc")
			err = RunMint(context.Background(), nil, MintOptions{
				Instance: "demo", Instrument: "XYZ", To: "bob::xyz", Amount: "100",
			})
			if !errors.Is(err, ErrUnresolvedLedgerEndpoint) {
				t.Fatalf("mint without a captured port: want ErrUnresolvedLedgerEndpoint, got %v", err)
			}
			check("unresolved endpoint", err.Error(), "restart it (`"+tc.prefix+" down demo` then `"+tc.prefix+" up demo`)")

			_, err = tokenBundleCommit(registry.NewState("demo", "does-not-exist-9.9.9"))
			if err == nil {
				t.Fatal("unknown Splice version must fail")
			}
			check("bundle commit", err.Error(), "upload the test-token DARs manually with "+manualDAR)

			seedBundleInstance(t, "demo", allAdminPorts())
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(srv.Close)
			swapDARBase(t, srv.URL)
			withDARDial(t, nil)
			_, err = ensureTokenDARs(context.Background(), bundleCreateOpts("demo"), io.Discard)
			if !errors.Is(err, ErrTokenDARUnavailable) {
				t.Fatalf("want ErrTokenDARUnavailable, got %v", err)
			}
			check("DAR unavailable", err.Error(), upV2, manualDAR)

			err = ensureLocalPartyRights(context.Background(), &fakeLedger{}, "demo", "app-user")
			if err == nil {
				t.Fatal("no local parties must fail")
			}
			check("onboarding", err.Error(), "check `"+tc.prefix+" status demo`")
			err = ensureLocalPartyRights(context.Background(), &fakeLedger{}, "", "app-user")
			if err == nil {
				t.Fatal("no local parties must fail")
			}
			check("onboarding without instance", err.Error(), "check `"+tc.prefix+" status <name>`")

			var out bytes.Buffer
			writeCreateSummary(&out, CreateOptions{Instance: "demo", Endpoint: "localhost:3901"},
				registry.TokenRef{Name: "Demo Token", Symbol: "DEMO", InstrumentID: "DEMO"}, nil)
			check("create summary", out.String(),
				"`"+tc.prefix+" token mint --instance demo --instrument DEMO --to <party> --amount <amount>` can now mint supply.")
		})
	}
}
