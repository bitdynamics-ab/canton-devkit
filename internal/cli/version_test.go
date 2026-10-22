package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func campaignFile(t *testing.T, env, contents string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "campaign-id")
	if contents != "" {
		if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)
	t.Setenv("DEVKIT_CAMPAIGN", env)
	if env == "" {
		if err := os.Unsetenv("DEVKIT_CAMPAIGN"); err != nil {
			t.Fatal(err)
		}
	}
	return file
}

func TestVersion_OutputUnchangedWhenCampaignDisabled(t *testing.T) {
	for _, env := range []string{"", "0", "off"} {
		file := campaignFile(t, env, "")
		var out, errOut bytes.Buffer
		code := New(&out, &errOut, "1.2.3", "abc1234").Run([]string{"version"})
		if code != 0 {
			t.Fatalf("DEVKIT_CAMPAIGN=%q: exit %d", env, code)
		}
		if got := out.String(); got != "canton-devkit 1.2.3 (abc1234)\n" {
			t.Errorf("DEVKIT_CAMPAIGN=%q: stdout = %q, want the plain version line", env, got)
		}
		if errOut.Len() != 0 {
			t.Errorf("DEVKIT_CAMPAIGN=%q: unexpected stderr %q", env, errOut.String())
		}
		if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("DEVKIT_CAMPAIGN=%q: a disabled build must not mint a code file", env)
		}
	}
}

func TestVersion_PrintsCampaignCodeWhenEnabled(t *testing.T) {
	campaignFile(t, "1", "CCT-ABCD2345\n")
	for _, args := range [][]string{{"version"}, {"--via-dpm", "version"}} {
		var out, errOut bytes.Buffer
		code := New(&out, &errOut, "v0.18.1-cctools.1", "f78650e").Run(args)
		if code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		want := "canton-devkit v0.18.1-cctools.1 (f78650e)\ncampaign code: CCT-ABCD2345\n"
		if got := out.String(); got != want {
			t.Errorf("%v: stdout = %q, want %q", args, got, want)
		}
		if errOut.Len() != 0 {
			t.Errorf("%v: unexpected stderr %q", args, errOut.String())
		}
	}
}

func TestRootVersionFlag_OutputUnchangedWhenCampaignDisabled(t *testing.T) {
	for _, env := range []string{"", "0", "off"} {
		for _, args := range [][]string{{"--version"}, {"-v"}, {"--via-dpm", "--version"}} {
			file := campaignFile(t, env, "")
			var out, errOut bytes.Buffer
			if code := New(&out, &errOut, "1.2.3", "abc1234").Run(args); code != 0 {
				t.Fatalf("DEVKIT_CAMPAIGN=%q %v: exit %d", env, args, code)
			}
			if got := out.String(); got != "canton-devkit version 1.2.3 (abc1234)\n" {
				t.Errorf("DEVKIT_CAMPAIGN=%q %v: stdout = %q, want cobra's plain version line", env, args, got)
			}
			if errOut.Len() != 0 {
				t.Errorf("DEVKIT_CAMPAIGN=%q %v: unexpected stderr %q", env, args, errOut.String())
			}
			if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("DEVKIT_CAMPAIGN=%q %v: a disabled build must not mint a code file", env, args)
			}
		}
	}
}

func TestRootVersionFlag_PrintsCampaignCodeLikeVersionCommand(t *testing.T) {
	campaignFile(t, "1", "CCT-ABCD2345\n")
	for _, args := range [][]string{{"--version"}, {"-v"}, {"--via-dpm", "--version"}} {
		var out, errOut bytes.Buffer
		if code := New(&out, &errOut, "v0.18.1-cctools.1", "f78650e").Run(args); code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		want := "canton-devkit version v0.18.1-cctools.1 (f78650e)\ncampaign code: CCT-ABCD2345\n"
		if got := out.String(); got != want {
			t.Errorf("%v: stdout = %q, want %q", args, got, want)
		}
		if errOut.Len() != 0 {
			t.Errorf("%v: unexpected stderr %q", args, errOut.String())
		}
	}

	var vout bytes.Buffer
	if code := New(&vout, &bytes.Buffer{}, "v0.18.1-cctools.1", "f78650e").Run([]string{"version"}); code != 0 {
		t.Fatalf("version exit %d", code)
	}
	var fout bytes.Buffer
	if code := New(&fout, &bytes.Buffer{}, "v0.18.1-cctools.1", "f78650e").Run([]string{"--version"}); code != 0 {
		t.Fatalf("--version exit %d", code)
	}
	vLines := strings.SplitN(vout.String(), "\n", 2)
	fLines := strings.SplitN(fout.String(), "\n", 2)
	if len(vLines) != 2 || len(fLines) != 2 || vLines[1] != fLines[1] {
		t.Errorf("--version and version must print the same campaign line: %q vs %q", fout.String(), vout.String())
	}
}

func TestRootVersionFlag_HelpWinsWithoutCampaignLine(t *testing.T) {
	campaignFile(t, "1", "CCT-ABCD2345\n")
	var out, errOut bytes.Buffer
	if code := New(&out, &errOut, "1.2.3", "").Run([]string{"--version", "--help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out.String(), "campaign code:") {
		t.Errorf("help output must not carry the campaign line, got %q", out.String())
	}
}

func TestRootVersionFlag_PersistFailureWarnsOnStderr(t *testing.T) {
	t.Setenv("DEVKIT_CAMPAIGN", "1")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", blockedByFile(t))
	var out, errOut bytes.Buffer
	if code := New(&out, &errOut, "1.2.3", "").Run([]string{"--version"}); code != 0 {
		t.Fatalf("persist failure must not fail --version, exit %d", code)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 || lines[0] != "canton-devkit version 1.2.3" || !strings.HasPrefix(lines[1], "campaign code: CCT-") {
		t.Errorf("stdout = %q, want version line plus campaign code line", out.String())
	}
	if n := strings.Count(errOut.String(), "change on restart"); n != 1 {
		t.Errorf("stderr should warn exactly once about the unsaved code, got %q", errOut.String())
	}
}

func blockedByFile(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocker, "campaign-id")
}

func blockedByReadOnlyDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not block writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return filepath.Join(dir, ".canton-devkit", "campaign-id")
}

func TestVersion_CampaignPersistFailureWarnsOnStderr(t *testing.T) {
	for name, blocked := range map[string]func(*testing.T) string{
		"path under a regular file": blockedByFile,
		"read-only home":            blockedByReadOnlyDir,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DEVKIT_CAMPAIGN", "1")
			t.Setenv("DEVKIT_CAMPAIGN_FILE", blocked(t))

			var out, errOut bytes.Buffer
			if code := New(&out, &errOut, "1.2.3", "").Run([]string{"version"}); code != 0 {
				t.Fatalf("persist failure must not fail the command, exit %d", code)
			}
			lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			if len(lines) != 2 || lines[0] != "canton-devkit 1.2.3" || !strings.HasPrefix(lines[1], "campaign code: CCT-") {
				t.Errorf("stdout = %q, want version line plus campaign code line", out.String())
			}
			if n := strings.Count(errOut.String(), "change on restart"); n != 1 {
				t.Errorf("stderr should warn exactly once about the unsaved code, got %q", errOut.String())
			}
		})
	}
}

func startUI(t *testing.T, a *App) string {
	t.Helper()
	var out, errOut lockedBuffer
	a.out, a.err = &out, &errOut
	root := a.buildRoot()
	root.SetArgs([]string{"localnet", "ui", "--port", "0"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("localnet ui did not stop after cancel")
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := out.String()
		if i := strings.Index(s, "http://"); i >= 0 {
			rest := s[i:]
			if j := strings.IndexAny(rest, " \t\n\x1b"); j >= 0 {
				return rest[:j]
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("localnet ui never printed its URL; stdout=%q stderr=%q", out.String(), errOut.String())
	return ""
}

func getVersionPayload(t *testing.T, base string) map[string]any {
	t.Helper()
	resp, err := http.Get(base + "api/version")
	if err != nil {
		t.Fatalf("GET /api/version: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode /api/version: %v", err)
	}
	return got
}

func TestLocalnetUI_VersionPayloadCarriesCLIIdentity(t *testing.T) {
	campaignFile(t, "0", "")
	for _, tc := range []struct {
		name       string
		version    string
		commit     string
		mode       InvocationMode
		wantVer    string
		wantPrefix string
	}{
		{"direct", "dev", "abc1234", Direct, "dev (abc1234)", "canton-devkit localnet"},
		{"via-dpm", "v0.18.1-cctools.1", "f78650e", ViaDPM, "v0.18.1-cctools.1 (f78650e)", "dpm localnet"},
		{"no-commit", "1.2.3", "", Direct, "1.2.3", "canton-devkit localnet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(nil, nil, tc.version, tc.commit)
			a.mode = tc.mode
			got := getVersionPayload(t, startUI(t, a))
			if got["version"] != tc.wantVer {
				t.Errorf("version = %v, want %q", got["version"], tc.wantVer)
			}
			if got["cli_prefix"] != tc.wantPrefix {
				t.Errorf("cli_prefix = %v, want %q", got["cli_prefix"], tc.wantPrefix)
			}
			if _, ok := got["campaign_code"]; ok {
				t.Errorf("campaign_code must be absent when DEVKIT_CAMPAIGN=0, got %v", got["campaign_code"])
			}

			var vout, verr bytes.Buffer
			vapp := New(&vout, &verr, tc.version, tc.commit)
			vapp.mode = tc.mode
			if code := vapp.Run([]string{"version"}); code != 0 {
				t.Fatalf("version exit %d", code)
			}
			if want := appName + " " + tc.wantVer + "\n"; vout.String() != want {
				t.Errorf("version printed %q, payload version %q must match its suffix", vout.String(), tc.wantVer)
			}
		})
	}
}

func TestLocalnetUI_CampaignCodeMatchesVersionCommand(t *testing.T) {
	campaignFile(t, "1", "CCT-ABCD2345\n")
	var out, errOut bytes.Buffer
	if code := New(&out, &errOut, "1.2.3", "").Run([]string{"version"}); code != 0 {
		t.Fatalf("version exit %d", code)
	}
	if !strings.Contains(out.String(), "campaign code: CCT-ABCD2345\n") {
		t.Fatalf("version stdout = %q, want the campaign code line", out.String())
	}
	got := getVersionPayload(t, startUI(t, New(nil, nil, "1.2.3", "")))
	if got["campaign_code"] != "CCT-ABCD2345" {
		t.Errorf("Web UI campaign_code = %v, CLI printed CCT-ABCD2345", got["campaign_code"])
	}
}
