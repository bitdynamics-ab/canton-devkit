package localnet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckComposeEnvCoverage(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	script := checkComposeEnvCoverageScript(t)

	cases := []struct {
		name        string
		projectName string
		compose     string
		files       map[string]string
		wantExit    int
		wantOutput  string
	}{
		{
			name:        "satisfied by adapter contract",
			projectName: "splice-0.6.4",
			compose:     `${LOCALNET_DIR} ${APP_USER_UI_PORT} ${COMMON_VAR} ${ALPHA_PROTOCOL_VERSION_ENV}`,
			files: map[string]string{
				"env/common.env": "COMMON_VAR=ok\n",
			},
			wantExit: 0,
		},
		{
			name:        "defaulted vars are optional",
			projectName: "splice-0.6.4",
			compose:     `${OPTIONAL-default} ${OPTIONAL_COLON:-default}`,
			wantExit:    0,
		},
		{
			name:        "required question operator is missing",
			projectName: "splice-0.6.4",
			compose:     `${REQUIRED?message}`,
			wantExit:    1,
			wantOutput:  "${REQUIRED}",
		},
		{
			name:        "required colon question operator is missing",
			projectName: "splice-0.6.4",
			compose:     `${REQUIRED_COLON:?message}`,
			wantExit:    1,
			wantOutput:  "${REQUIRED_COLON}",
		},
		{
			name:        "non adapter env files do not satisfy refs",
			projectName: "splice-0.6.4",
			compose:     `${POSTGRES_ONLY}`,
			files: map[string]string{
				"env/postgres.env": "POSTGRES_ONLY=not-loaded-by-adapter\n",
			},
			wantExit:   1,
			wantOutput: "${POSTGRES_ONLY}",
		},
		{
			name:        "alpha env is not provided for v05",
			projectName: "splice-0.5.18",
			compose:     `${ALPHA_PROTOCOL_VERSION_ENV}`,
			wantExit:    1,
			wantOutput:  "${ALPHA_PROTOCOL_VERSION_ENV}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cacheRoot := t.TempDir()
			projectDir := filepath.Join(cacheRoot, tc.projectName)
			writeCoverageFixtureFile(t, projectDir, "compose.yaml", tc.compose)
			for rel, body := range tc.files {
				writeCoverageFixtureFile(t, projectDir, rel, body)
			}

			cmd := exec.Command("bash", script, cacheRoot)
			out, err := cmd.CombinedOutput()
			gotExit := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					gotExit = exitErr.ExitCode()
				} else {
					t.Fatalf("run script: %v\n%s", err, out)
				}
			}
			if gotExit != tc.wantExit {
				t.Fatalf("exit = %d, want %d\noutput:\n%s", gotExit, tc.wantExit, out)
			}
			if tc.wantOutput != "" && !strings.Contains(string(out), tc.wantOutput) {
				t.Fatalf("output missing %q:\n%s", tc.wantOutput, out)
			}
		})
	}
}

func checkComposeEnvCoverageScript(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(wd, "..", "..", "scripts", "check-compose-env-coverage.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("script not found: %v", err)
	}
	return script
}

func writeCoverageFixtureFile(t *testing.T, root string, rel string, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
