package token

import (
	"fmt"
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
		{"", []string{"`dpm localnet up --version 0.6.12`", "`dpm localnet dar upload`"}, "canton-devkit localnet"},
		{"canton-devkit localnet", []string{"`canton-devkit localnet up --version 0.6.12`", "`canton-devkit localnet dar upload`"}, "dpm localnet"},
	} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			if tc.prefix != "" {
				t.Cleanup(cliprefix.Set(tc.prefix))
			}
			msg := darUnavailableError("splice-test-token-v2", "0.6.4").Error()
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
