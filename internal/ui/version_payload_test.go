package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func fetchVersionPayload(t *testing.T) map[string]any {
	t.Helper()
	assets, err := AssetsHandler()
	if err != nil {
		t.Fatalf("AssetsHandler: %v", err)
	}
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	resp, err := http.Get("http://" + addr + "/api/version")
	if err != nil {
		t.Fatalf("GET /api/version: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/version status = %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func campaignEnv(t *testing.T, value string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "campaign-id")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)
	t.Setenv("DEVKIT_CAMPAIGN", value)
	if value == "" {
		if err := os.Unsetenv("DEVKIT_CAMPAIGN"); err != nil {
			t.Fatal(err)
		}
	}
	return file
}

func TestVersion_NoCampaignCodeByDefault(t *testing.T) {
	file := campaignEnv(t, "")
	got := fetchVersionPayload(t)
	if v, ok := got["campaign_code"]; ok {
		t.Errorf("default build must not emit campaign_code, got %v", v)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("default build must not mint a campaign code file")
	}
}

func TestVersion_CampaignCodeWhenEnabled(t *testing.T) {
	file := campaignEnv(t, "1")
	got := fetchVersionPayload(t)
	code, ok := got["campaign_code"].(string)
	if !ok {
		t.Fatalf("DEVKIT_CAMPAIGN=1 must emit campaign_code, payload = %v", got)
	}
	if !regexp.MustCompile(`^CCT-[A-Z2-7]{8}$`).MatchString(code) {
		t.Errorf("campaign_code = %q, want ^CCT-[A-Z2-7]{8}$", code)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("campaign code not persisted: %v", err)
	}
	if string(b) != code+"\n" {
		t.Errorf("persisted code %q differs from served %q", b, code)
	}
	if again := fetchVersionPayload(t)["campaign_code"]; again != code {
		t.Errorf("campaign_code must be stable across requests: %q then %v", code, again)
	}
}
