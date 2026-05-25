package localnet

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// seedEnvInstance writes a registry record with ports + credentials
// so collectEnv has something to read. Matches the shape `localnet
// up` would have produced on a real bring-up — see internal/localnet
// /up.go where Ports/Credentials are populated.
func seedEnvInstance(t *testing.T, name string) {
	t.Helper()
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DockerNetwork = name
	s.ContainerPrefix = name + "-"
	s.ProjectDir = t.TempDir()
	s.DataDir = "/test/" + name
	s.Status = registry.StatusRunning
	s.Ports = map[string]int{
		"app_user_ui":     4485,
		"app-provider-ui": 3485, // hyphen variant — must normalise
		"postgres":        5432,
	}
	s.Credentials = map[string]registry.Credential{
		"sv": {
			Role:     "sv",
			User:     "sv-user",
			Audience: "sv-aud",
			JWT:      "eyJ.svtoken.sig",
		},
		"app-user": {
			Role:     "app-user",
			User:     "au-user",
			Audience: "au-aud",
			JWT:      "eyJ.aut.sig",
		},
	}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestEnv_ShellOutputContainsExpectedKeys is the smoke check that
// `eval $(dpm localnet env --name X)` would set the env vars a
// caller reasonably expects to find.
func TestEnv_ShellOutputContainsExpectedKeys(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvInstance(t, "demo")

	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, want := range []string{
		"CANTON_INSTANCE=demo",
		"CANTON_SPLICE_VERSION=0.6.4",
		"CANTON_AUTH_FILE=/test/demo/auth.json",
		"CANTON_APP_USER_UI_PORT=4485",
		"CANTON_APP_PROVIDER_UI_PORT=3485", // hyphen normalised
		"CANTON_POSTGRES_PORT=5432",
		"CANTON_SV_JWT=eyJ.svtoken.sig",
		"CANTON_SV_USER=sv-user",
		"CANTON_SV_AUDIENCE=sv-aud",
		"CANTON_APP_USER_JWT=eyJ.aut.sig",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out.String())
		}
	}
}

// TestEnv_OutputIsSortedAndStable proves the shell output is
// reproducible: same inputs → identical bytes, including line order.
// Without this guarantee the output diffs noisily and downstream
// CI snapshot tests are flaky.
func TestEnv_OutputIsSortedAndStable(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvInstance(t, "demo")

	cmd1 := buildEnv()
	var out1 bytes.Buffer
	cmd1.SetOut(&out1)
	cmd1.SetErr(&out1)
	cmd1.SetArgs([]string{"--name", "demo"})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("first execute: %v", err)
	}

	cmd2 := buildEnv()
	var out2 bytes.Buffer
	cmd2.SetOut(&out2)
	cmd2.SetErr(&out2)
	cmd2.SetArgs([]string{"--name", "demo"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("second execute: %v", err)
	}

	if out1.String() != out2.String() {
		t.Errorf("output not stable across calls:\nfirst:\n%s\nsecond:\n%s", out1.String(), out2.String())
	}
}

// TestEnv_JSONShape pins the --format=json contract. The Web UI
// handler (BIT-131) will call collectEnv directly, but a CLI
// consumer that parses the JSON also needs the shape locked in.
func TestEnv_JSONShape(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvInstance(t, "demo")

	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "demo", "--format=json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var got EnvExport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if got.Instance != "demo" {
		t.Errorf("Instance = %q, want demo", got.Instance)
	}
	if got.Vars["CANTON_SPLICE_VERSION"] != "0.6.4" {
		t.Errorf("Vars[CANTON_SPLICE_VERSION] = %q, want 0.6.4", got.Vars["CANTON_SPLICE_VERSION"])
	}
	if got.Vars["CANTON_SV_JWT"] != "eyJ.svtoken.sig" {
		t.Errorf("Vars[CANTON_SV_JWT] = %q, want eyJ.svtoken.sig", got.Vars["CANTON_SV_JWT"])
	}
}

// TestEnv_NotFoundIsUserError covers the unknown-instance path.
func TestEnv_NotFoundIsUserError(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	cmd := buildEnv()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--name", "ghost"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown instance")
	}
	if !strings.Contains(errBuf.String(), `"ghost"`) {
		t.Errorf("stderr should name the missing instance, got %q", errBuf.String())
	}
}

// TestEnv_RejectsUnknownFormat is the input-validation floor — a
// typo in --format must surface clearly.
func TestEnv_RejectsUnknownFormat(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvInstance(t, "demo")

	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--name", "demo", "--format=xml"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown --format")
	}
}

// TestPortEnvKey covers the normalisation directly since the
// hyphen-vs-underscore corner is the trickiest part — different
// adapter versions emit different shapes and downstream consumers
// must see one canonical form.
func TestPortEnvKey(t *testing.T) {
	cases := map[string]string{
		"app_user_ui":     "CANTON_APP_USER_UI_PORT",
		"app-provider-ui": "CANTON_APP_PROVIDER_UI_PORT",
		"postgres":        "CANTON_POSTGRES_PORT",
		"SV":              "CANTON_SV_PORT",
	}
	for in, want := range cases {
		if got := portEnvKey(in); got != want {
			t.Errorf("portEnvKey(%q) = %q, want %q", in, got, want)
		}
	}
}
