package localnet

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestValidateCredsOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    CredsOptions
		wantErr string
	}{
		{
			name:    "raw requires role",
			opts:    CredsOptions{Format: "raw"},
			wantErr: "--format raw requires --role",
		},
		{
			name:    "invalid format",
			opts:    CredsOptions{Format: "yaml"},
			wantErr: "--format must be one of",
		},
		{
			name:    "invalid role",
			opts:    CredsOptions{Format: "raw", Role: "appuser"},
			wantErr: "--role must be one of",
		},
		{
			name: "valid raw role",
			opts: CredsOptions{Format: "raw", Role: "app-user"},
		},
		{
			name: "valid all roles table",
			opts: CredsOptions{Format: "table"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCredsOptions(&tt.opts)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateCredsOptions() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateCredsOptions() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunCreds_TableIncludesJWTs(t *testing.T) {
	seedCredsInstance(t, "creds-table")

	var out, errBuf bytes.Buffer
	code := RunCreds(&out, &errBuf, &CredsOptions{Name: "creds-table", Format: "table"})
	if code != ExitSuccess {
		t.Fatalf("RunCreds = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	stdout := out.String()
	for _, want := range []string{"ROLE", "USER", "AUDIENCE", "JWT", "app-user", "app-provider", "sv"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table output missing %q: %q", want, stdout)
		}
	}
	for _, token := range []string{"app-user-token", "app-provider-token", "sv-token"} {
		if !strings.Contains(stdout, token) {
			t.Errorf("table output missing JWT %q: %q", token, stdout)
		}
	}
}

func TestRunCreds_EnvJSONAndRawFormats(t *testing.T) {
	seedCredsInstance(t, "creds-formats")

	t.Run("env", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		code := RunCreds(&out, &errBuf, &CredsOptions{Name: "creds-formats", Format: "env", Role: "app-user"})
		if code != ExitSuccess {
			t.Fatalf("RunCreds env = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
		}
		want := "export AUTH_APP_USER_TOKEN=\"app-user-token\"\n"
		if out.String() != want {
			t.Fatalf("env output = %q, want %q", out.String(), want)
		}
	})

	t.Run("json", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		code := RunCreds(&out, &errBuf, &CredsOptions{Name: "creds-formats", Format: "json", Role: "sv"})
		if code != ExitSuccess {
			t.Fatalf("RunCreds json = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
		}
		var got []registry.Credential
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("json output did not decode: %v\n%s", err, out.String())
		}
		if len(got) != 1 || got[0].Role != "sv" || got[0].JWT != "sv-token" {
			t.Fatalf("json output = %#v, want sv credential", got)
		}
	})

	t.Run("raw", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		code := RunCreds(&out, &errBuf, &CredsOptions{Name: "creds-formats", Format: "raw", Role: "app-provider"})
		if code != ExitSuccess {
			t.Fatalf("RunCreds raw = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
		}
		if out.String() != "app-provider-token\n" {
			t.Fatalf("raw output = %q", out.String())
		}
	})
}

func TestRunCreds_MissingDataIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	var out, errBuf bytes.Buffer
	code := RunCreds(&out, &errBuf, &CredsOptions{Name: "missing", Format: "table"})
	if code != ExitUserError {
		t.Fatalf("RunCreds missing = %d, want ExitUserError; stdout=%q stderr=%q", code, out.String(), errBuf.String())
	}

	state := registry.NewState("empty-creds", "0.6.4")
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}
	out.Reset()
	errBuf.Reset()
	code = RunCreds(&out, &errBuf, &CredsOptions{Name: "empty-creds", Format: "table"})
	if code != ExitUserError {
		t.Fatalf("RunCreds empty creds = %d, want ExitUserError; stdout=%q stderr=%q", code, out.String(), errBuf.String())
	}
}

func seedCredsInstance(t *testing.T, name string) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	state := registry.NewState(name, "0.6.4")
	state.Status = registry.StatusRunning
	state.Credentials = map[string]registry.Credential{
		"app-provider": {Role: "app-provider", User: "app-provider-user", Audience: "app-provider-audience", JWT: "app-provider-token"},
		"app-user":     {Role: "app-user", User: "app-user-user", Audience: "app-user-audience", JWT: "app-user-token"},
		"sv":           {Role: "sv", User: "sv-user", Audience: "sv-audience", JWT: "sv-token"},
	}
	if err := registry.Write(state); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}
}
