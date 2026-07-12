package dar

import (
	"strings"
	"testing"
)

// TestBuildEnv is the regression pin for issue #230: under `dpm localnet
// …`, DPM sets DPM_RESOLUTION_FILE to a temp file it already wrote. The
// child `dpm build` DevKit shells out to must NOT inherit it, or it
// aborts with "file exists". buildEnv() drops that var while preserving
// everything else.
func TestBuildEnv(t *testing.T) {
	t.Setenv("DPM_RESOLUTION_FILE", "/tmp/should-not-leak.yaml")
	t.Setenv("CDK_BUILDENV_SENTINEL", "keep-me")

	env := buildEnv()

	var sawSentinel bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "DPM_RESOLUTION_FILE=") {
			t.Errorf("buildEnv() leaked DPM_RESOLUTION_FILE into the child env: %q", kv)
		}
		if kv == "CDK_BUILDENV_SENTINEL=keep-me" {
			sawSentinel = true
		}
	}
	if !sawSentinel {
		t.Error("buildEnv() dropped an unrelated env var (CDK_BUILDENV_SENTINEL) — it should only strip DPM_RESOLUTION_FILE")
	}
}
