package localnet

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui"
)

func runUI(t *testing.T, info ui.BuildInfo) (string, *safeBuffer) {
	t.Helper()
	cmd := buildUI(info)
	var out, errBuf safeBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--port", "0"})
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("ui command did not stop after cancel")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if u := extractURL(out.String()); u != "" {
			return u, &errBuf
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("URL never appeared; stdout=%q stderr=%q", out.String(), errBuf.String())
	return "", nil
}

func versionPayload(t *testing.T, base string) map[string]any {
	t.Helper()
	resp, err := http.Get(base + "api/version")
	if err != nil {
		t.Fatalf("GET /api/version: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestUI_ServesInjectedBuildInfo(t *testing.T) {
	t.Setenv("DEVKIT_CAMPAIGN", "0")
	info := ui.BuildInfo{Version: "v0.18.1-cctools.1 (f78650e)", CLIPrefix: "dpm localnet"}
	url, _ := runUI(t, info)
	got := versionPayload(t, url)
	if got["version"] != info.Version || got["cli_prefix"] != info.CLIPrefix {
		t.Errorf("payload = %v, want version %q and cli_prefix %q", got, info.Version, info.CLIPrefix)
	}
}

func TestUI_WarnsOnceWhenCampaignCodeCannotBeSaved(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_CAMPAIGN", "1")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", filepath.Join(blocker, "campaign-id"))

	url, errBuf := runUI(t, ui.BuildInfo{})
	first, _ := versionPayload(t, url)["campaign_code"].(string)
	second, _ := versionPayload(t, url)["campaign_code"].(string)
	if !regexp.MustCompile(`^CCT-[A-Z2-7]{8}$`).MatchString(first) {
		t.Fatalf("campaign_code = %q, want a well-formed code even when it cannot be saved", first)
	}
	if second != first {
		t.Errorf("unsaved code must stay stable for the server's lifetime: %q then %q", first, second)
	}
	if n := strings.Count(errBuf.String(), "campaign code not saved"); n != 1 {
		t.Errorf("want exactly one persist warning on stderr, got %d in %q", n, errBuf.String())
	}
}
