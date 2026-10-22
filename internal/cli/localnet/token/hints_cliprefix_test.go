package token

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/spf13/pflag"
)

func TestTokenCLIHints_FollowCLIPrefix(t *testing.T) {
	for _, tc := range []struct{ set, prefix, other string }{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			if tc.set != "" {
				t.Cleanup(cliprefix.Set(tc.set))
			}
			t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
			s := registry.NewState("demo", "0.6.4")
			s.Status = registry.StatusRunning
			if err := registry.Write(s); err != nil {
				t.Fatal(err)
			}

			cmd := buildList()
			var out, errBuf bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errBuf)
			cmd.SetArgs([]string{"--instance", "demo"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("token ls: %v (stderr=%q)", err, errBuf.String())
			}
			want := "No instruments. Create one with `" + tc.prefix + " token create --instance demo`."
			if !strings.Contains(out.String(), want) {
				t.Errorf("token ls output missing %q:\n%s", want, out.String())
			}
			if strings.Contains(out.String(), tc.other) {
				t.Errorf("token ls must not mention %q:\n%s", tc.other, out.String())
			}

			flag := buildBalance().Flags().Lookup("token")
			varname, usage := pflag.UnquoteUsage(flag)
			if varname != "string" {
				t.Errorf("--token placeholder = %q, want %q (backticks in the usage hijack it)", varname, "string")
			}
			if !strings.Contains(usage, "via "+tc.prefix+" creds machinery") || strings.Contains(usage, tc.other) {
				t.Errorf("--token usage = %q, want it to name %q", usage, tc.prefix+" creds")
			}
		})
	}
}
