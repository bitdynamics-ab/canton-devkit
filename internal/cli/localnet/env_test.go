package localnet

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	corelocalnet "github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

var errEnvWriter = errors.New("env writer failed")

type failingEnvWriter struct{}

func (failingEnvWriter) Write([]byte) (int, error) {
	return 0, errEnvWriter
}

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
		"app_user_ui":                 4485,
		"app-provider-ui":             3485,
		"sv_ui":                       4480,
		"postgres":                    5432,
		"participant_ledger_app-user": 2901,
		"participant_json_app-user":   2975,
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
	// Real on-ledger party ids, keyed by the role alias. Distinct
	// from the credential User (a ledger-api user name) — the env
	// export must carry both without conflating them.
	s.Parties = map[string]registry.PartyRef{
		"app-user": {
			Alias:   "app-user",
			PartyID: "app-user::1220abc",
			Role:    "app-user",
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
		"export CANTON_INSTANCE='demo'",
		"export CANTON_SPLICE_VERSION='0.6.4'",
		// Use filepath.Join so the assertion holds
		// on Windows (\) and POSIX (/) -- mirrors the
		// production filepath.Join in env.go::collectEnv.
		"export CANTON_AUTH_FILE='" + filepath.Join("/test/demo", "auth.json") + "'",
		"export CANTON_APP_USER_UI_PORT='4485'",
		"export CANTON_APP_PROVIDER_UI_PORT='3485'",
		"export CANTON_POSTGRES_PORT='5432'",
		// Participant Ledger/JSON API ports a dApp dials directly.
		"export CANTON_PARTICIPANT_LEDGER_APP_USER_PORT='2901'",
		"export CANTON_PARTICIPANT_JSON_APP_USER_PORT='2975'",
		// Scan UI surfaced explicitly with the scan.localhost vhost.
		"export CANTON_SCAN_UI_URL='http://scan.localhost:4480'",
		"export CANTON_SV_JWT='<redacted>'",
		"export CANTON_SV_USER='sv-user'",
		"export CANTON_SV_AUDIENCE='sv-aud'",
		"export CANTON_APP_USER_JWT='<redacted>'",
		// Real on-ledger party id, distinct from the user name.
		"export CANTON_APP_USER_PARTY='app-user::1220abc'",
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

	// Must contain the hostile substring AS A LITERAL inside
	// single quotes. The `$()` and backticks must NOT appear
	// outside surrounding quotes.
	wantLiteral := "export CANTON_AUTH_FILE='" + filepath.Join("/tmp/$(rm -rf ~); echo pwned `id`", "auth.json") + "'"
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

	if !strings.Contains(out.String(), "export CANTON_SV_JWT='eyJ.svtoken.sig'") {
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

	// Every backtick is escaped per dotenv spec (some parsers
	// treat ` as command-substitution); want \`tick\`.
	// Build via filepath.Join + dotenvQuote so the assertion is
	// portable across separator conventions.
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

	var got apitypes.EnvExport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, out.String())
	}
	if got.SchemaVersion != apitypes.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, apitypes.SchemaVersion)
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
	var got apitypes.EnvExport
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
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown instance")
	}
	if exit, ok := err.(corelocalnet.ExitCodeError); !ok || int(exit) != corelocalnet.ExitUserError {
		t.Fatalf("error = %T %[1]v, want ExitUserError", err)
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
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown --format")
	}
	if exit, ok := err.(corelocalnet.ExitCodeError); !ok || int(exit) != corelocalnet.ExitUserError {
		t.Fatalf("error = %T %[1]v, want ExitUserError", err)
	}
	if !strings.Contains(out.String(), `--format must be shell, dotenv, or json (got "xml")`) {
		t.Errorf("stderr = %q, want format validation message", out.String())
	}
}

func TestEnv_RejectsInvalidNameAsUserError(t *testing.T) {
	cmd := buildEnv()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--name", "bad/name"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if exit, ok := err.(corelocalnet.ExitCodeError); !ok || int(exit) != corelocalnet.ExitUserError {
		t.Fatalf("error = %T %[1]v, want ExitUserError", err)
	}
	if out.String() == "" {
		t.Fatal("expected validation message on stderr")
	}
}

func TestEnv_WriterErrorsPropagate(t *testing.T) {
	ex := apitypes.EnvExport{
		SchemaVersion: apitypes.SchemaVersion,
		Instance:      "demo",
		Vars:          map[string]string{"CANTON_INSTANCE": "demo"},
	}
	for name, write := range map[string]func(io.Writer, apitypes.EnvExport) error{
		"shell":  writeEnvShell,
		"dotenv": writeEnvDotenv,
		"json":   writeEnvJSON,
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(failingEnvWriter{}, ex); !errors.Is(err, errEnvWriter) {
				t.Fatalf("error = %v, want %v", err, errEnvWriter)
			}
		})
	}
}

func TestEnv_CommandWriterErrorPropagates(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedEnvInstance(t, "demo")

	cmd := buildEnv()
	cmd.SetOut(failingEnvWriter{})
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--name", "demo"})
	if err := cmd.Execute(); !errors.Is(err, errEnvWriter) {
		t.Fatalf("error = %v, want %v", err, errEnvWriter)
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
