package localnet

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

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
		"app-provider-ui": 3485,
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

func TestEnv_ShellOutputIsPosixQuoted(t *testing.T) {
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
		"CANTON_INSTANCE='demo'",
		"CANTON_SPLICE_VERSION='0.6.4'",
		"CANTON_AUTH_FILE='" + filepath.Join("/test/demo", "auth.json") + "'",
		"CANTON_APP_USER_UI_PORT='4485'",
		"CANTON_APP_PROVIDER_UI_PORT='3485'",
		"CANTON_POSTGRES_PORT='5432'",
		"CANTON_SV_JWT='<redacted>'",
		"CANTON_SV_USER='sv-user'",
		"CANTON_SV_AUDIENCE='sv-aud'",
		"CANTON_APP_USER_JWT='<redacted>'",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out.String())
		}
	}
}

func TestEnv_ShellQuotesNeutraliseInjection(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("hostile", "0.6.4")
	s.ComposeProject = "canton-hostile"
	s.DockerNetwork = "hostile"
	s.ContainerPrefix = "hostile-"
	s.ProjectDir = t.TempDir()
	s.DataDir = "/tmp/$(rm -rf ~); echo pwned `id`"
	s.Status = registry.StatusRunning
	s.Ports = map[string]int{"app_user_ui": 4485}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "hostile"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	wantLiteral := "CANTON_AUTH_FILE='" + filepath.Join("/tmp/$(rm -rf ~); echo pwned `id`", "auth.json") + "'"
	if !strings.Contains(out.String(), wantLiteral) {
		t.Errorf("hostile DataDir not POSIX-quoted; output:\n%s\nwant substring: %s", out.String(), wantLiteral)
	}
}

func TestEnv_JWTRedactionDefault(t *testing.T) {
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

	for _, leaked := range []string{"eyJ.svtoken.sig", "eyJ.aut.sig"} {
		if strings.Contains(out.String(), leaked) {
			t.Errorf("raw JWT %q leaked into default output:\n%s", leaked, out.String())
		}
	}
	if !strings.Contains(out.String(), "<redacted>") {
		t.Errorf("expected <redacted> placeholder, got\n%s", out.String())
	}
}

func TestEnv_IncludeJWTOptIn(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvInstance(t, "demo")

	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "demo", "--include-jwt"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out.String(), "CANTON_SV_JWT='eyJ.svtoken.sig'") {
		t.Errorf("expected raw JWT under --include-jwt, got\n%s", out.String())
	}
}

func TestEnv_DotenvUsesDoubleQuoteAndEscapes(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState("demo", "0.6.4")
	s.ComposeProject = "canton-demo"
	s.DockerNetwork = "demo"
	s.ContainerPrefix = "demo-"
	s.ProjectDir = t.TempDir()
	s.DataDir = `/tmp/with"quote/and$dollar/and\back/and` + "`tick`"
	s.Status = registry.StatusRunning
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "demo", "--format=dotenv"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	want := "CANTON_AUTH_FILE=" + dotenvQuote(filepath.Join(s.DataDir, "auth.json"))
	if !strings.Contains(out.String(), want) {
		t.Errorf("dotenv escaping wrong\nfull:\n%s\nwant substring: %s", out.String(), want)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":            "''",
		"plain":       "'plain'",
		"with space":  "'with space'",
		"with$dollar": "'with$dollar'",
		"with'quote":  `'with'\''quote'`,
		"a'b'c":       `'a'\''b'\''c'`,
		"$(rm -rf ~)": "'$(rm -rf ~)'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDotenvQuote(t *testing.T) {
	cases := map[string]string{
		"":            `""`,
		"plain":       `"plain"`,
		`with"quote`:  `"with\"quote"`,
		`with$dollar`: `"with\$dollar"`,
		`with\back`:   `"with\\back"`,
		"with`tick`":  "\"with\\`tick\\`\"",
	}
	for in, want := range cases {
		if got := dotenvQuote(in); got != want {
			t.Errorf("dotenvQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

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
	if got.Vars["CANTON_SV_JWT"] != "<redacted>" {
		t.Errorf("Vars[CANTON_SV_JWT] = %q, want <redacted>", got.Vars["CANTON_SV_JWT"])
	}
}

func TestEnv_JSONIncludeJWT(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvInstance(t, "demo")

	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "demo", "--format=json", "--include-jwt"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got EnvExport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if got.Vars["CANTON_SV_JWT"] != "eyJ.svtoken.sig" {
		t.Errorf("expected raw JWT under --include-jwt, got %q", got.Vars["CANTON_SV_JWT"])
	}
}

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
