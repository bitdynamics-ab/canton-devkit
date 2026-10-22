package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
)

func TestRun_CLIMessagesFollowInvocationMode(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	for _, tc := range []struct {
		name    string
		args    []string
		want    string
		notWant string
	}{
		{"direct", []string{"localnet", "status", "ghost"}, "Run `canton-devkit localnet list` to see available instances.", "dpm localnet"},
		{"via-dpm", []string{"--via-dpm", "localnet", "status", "ghost"}, "Run `dpm localnet list` to see available instances.", "canton-devkit localnet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := New(&out, &errOut, "test", "").Run(tc.args); code == 0 {
				t.Fatalf("status on an unknown instance must fail, stderr=%q", errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("stderr = %q, want %q", errOut.String(), tc.want)
			}
			if strings.Contains(errOut.String(), tc.notWant) {
				t.Errorf("stderr = %q must not mention %q", errOut.String(), tc.notWant)
			}
			if got := cliprefix.Get(); got != cliprefix.Default {
				t.Errorf("Run must restore the shared prefix on return, got %q", got)
			}
		})
	}
}

func TestRun_SubcommandHelpFollowsInvocationMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    []string
		prefix  string
		notWant string
	}{
		{"direct", nil, "canton-devkit localnet", "dpm localnet"},
		{"via-dpm", []string{"--via-dpm"}, "dpm localnet", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for verb, want := range map[string][]string{
				"down":      {tc.prefix + " up <name>", "`" + tc.prefix + " stop <name>`", "`" + tc.prefix + " remove <name>`"},
				"env":       {`eval "$(` + tc.prefix + ` env hubble)"`, tc.prefix + ` env ci --format github-env >> "$GITHUB_ENV"`},
				"dar watch": {"Base URL of a running " + tc.prefix + " ui for hot-deploy events"},
			} {
				var out, errOut bytes.Buffer
				args := append(append(append([]string{}, tc.mode...), "localnet"), strings.Fields(verb)...)
				args = append(args, "--help")
				if code := New(&out, &errOut, "test", "").Run(args); code != 0 {
					t.Fatalf("%v: exit %d, stderr=%q", args, code, errOut.String())
				}
				for _, w := range want {
					if !strings.Contains(out.String(), w) {
						t.Errorf("%s --help missing %q:\n%s", verb, w, out.String())
					}
				}
				if tc.notWant != "" && strings.Contains(out.String(), tc.notWant) {
					t.Errorf("%s --help must not mention %q:\n%s", verb, tc.notWant, out.String())
				}
			}
		})
	}
}

func TestLocalnetUI_HandlerHintsMatchCLIForInvocationMode(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	campaignFile(t, "0", "")
	for _, tc := range []struct {
		name string
		mode InvocationMode
		args []string
		want string
	}{
		{"direct", Direct, []string{"localnet", "status", "ghost"}, "Run `canton-devkit localnet list` to see available instances."},
		{"via-dpm", ViaDPM, []string{"--via-dpm", "localnet", "status", "ghost"}, "Run `dpm localnet list` to see available instances."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(nil, nil, "test", "")
			a.mode = tc.mode
			base := startUI(t, a)

			req, err := http.NewRequest(http.MethodPost, base+"api/instances/ghost/snapshot", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", strings.TrimSuffix(base, "/"))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST snapshot: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("POST snapshot status = %d, want 404", resp.StatusCode)
			}
			var body struct {
				Remediation []string `json:"remediation"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(body.Remediation) != 1 || body.Remediation[0] != tc.want {
				t.Errorf("Web UI remediation = %q, want [%q]", body.Remediation, tc.want)
			}
			if got := getVersionPayload(t, base)["cli_prefix"]; got != a.mode.cliPrefix() {
				t.Errorf("cli_prefix = %v, want %q", got, a.mode.cliPrefix())
			}

			var out, errOut bytes.Buffer
			_ = New(&out, &errOut, "test", "").Run(tc.args)
			if !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("CLI stderr = %q, want the same hint %q", errOut.String(), tc.want)
			}
		})
	}
}
