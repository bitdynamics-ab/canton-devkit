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

func TestVersion_CampaignPersistFailureWarnsOnStderr(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_CAMPAIGN", "1")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", filepath.Join(blocker, "campaign-id"))

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
