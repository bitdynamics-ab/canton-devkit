package localnet

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestParseComposeImagesJSON pins the parse of `docker compose images
// --format json` into a repository:tag → digest map, including the
// dedupe of an image used by multiple containers and the skipping of
// entries with no repository/ID.
func TestParseComposeImagesJSON(t *testing.T) {
	raw := []byte(`[
	  {"ID":"sha256:aaa","ContainerName":"x-canton","Repository":"ghcr.io/ds/canton","Tag":"0.6.4"},
	  {"ID":"sha256:bbb","ContainerName":"x-wallet-app-provider","Repository":"ghcr.io/ds/wallet-web-ui","Tag":"0.6.4"},
	  {"ID":"sha256:bbb","ContainerName":"x-wallet-app-user","Repository":"ghcr.io/ds/wallet-web-ui","Tag":"0.6.4"},
	  {"ID":"sha256:ccc","ContainerName":"x-built","Repository":"","Tag":"latest"},
	  {"ID":"","ContainerName":"x-noid","Repository":"ghcr.io/ds/scan-web-ui","Tag":"0.6.4"}
	]`)

	got, err := parseComposeImagesJSON(raw)
	if err != nil {
		t.Fatalf("parseComposeImagesJSON: %v", err)
	}
	want := map[string]string{
		"ghcr.io/ds/canton:0.6.4":        "sha256:aaa",
		"ghcr.io/ds/wallet-web-ui:0.6.4": "sha256:bbb", // deduped across two containers
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseComposeImagesJSON = %v, want %v", got, want)
	}
}

// TestParseComposeImagesJSON_EmptyAndGarbage covers the best-effort
// contract: empty output is (nil, nil); malformed JSON is an error so
// CaptureImageDigests treats it as "couldn't capture" and skips.
func TestParseComposeImagesJSON_EmptyAndGarbage(t *testing.T) {
	if got, err := parseComposeImagesJSON([]byte("  \n")); err != nil || got != nil {
		t.Errorf("empty: got (%v,%v), want (nil,nil)", got, err)
	}
	if got, err := parseComposeImagesJSON([]byte("not json")); err == nil || got != nil {
		t.Errorf("garbage: got (%v,%v), want (nil, error)", got, err)
	}
	// Valid JSON, but every entry lacks repo/ID → nil (nothing to track).
	if got, err := parseComposeImagesJSON([]byte(`[{"ID":"","Repository":""}]`)); err != nil || got != nil {
		t.Errorf("all-empty entries: got (%v,%v), want (nil,nil)", got, err)
	}
}

// TestDiffImageDigests pins the drift logic: only keys present in BOTH
// with a CHANGED digest are reported. Added/dropped images (key on one
// side only) are not drift — that's a profile toggle, not a republish.
func TestDiffImageDigests(t *testing.T) {
	prior := map[string]string{
		"ghcr.io/ds/canton:0.6.4":  "sha256:aaa",
		"ghcr.io/ds/splice:0.6.4":  "sha256:bbb",
		"ghcr.io/ds/dropped:0.6.4": "sha256:ddd",
	}
	current := map[string]string{
		"ghcr.io/ds/canton:0.6.4": "sha256:aaa",     // unchanged
		"ghcr.io/ds/splice:0.6.4": "sha256:CHANGED", // republished → drift
		"ghcr.io/ds/added:0.6.4":  "sha256:eee",     // newly added → not drift
	}
	got := DiffImageDigests(prior, current)
	want := []string{"ghcr.io/ds/splice:0.6.4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiffImageDigests = %v, want %v", got, want)
	}

	// No prior (first up) or no current (capture failed) → no drift.
	if got := DiffImageDigests(nil, current); got != nil {
		t.Errorf("DiffImageDigests(nil, current) = %v, want nil", got)
	}
	if got := DiffImageDigests(prior, nil); got != nil {
		t.Errorf("DiffImageDigests(prior, nil) = %v, want nil", got)
	}
}

// TestCaptureImageDigests_Seam exercises the capture path via the
// composeImagesCmd seam: a successful JSON response maps to digests, and
// a command error returns nil (best-effort, never fails the bring-up).
func TestCaptureImageDigests_Seam(t *testing.T) {
	orig := composeImagesCmd
	defer func() { composeImagesCmd = orig }()

	composeImagesCmd = func(_ context.Context, project string) ([]byte, error) {
		if project != "canton-demo" {
			t.Errorf("project = %q, want canton-demo", project)
		}
		return []byte(`[{"ID":"sha256:zzz","Repository":"ghcr.io/ds/canton","Tag":"0.6.4"}]`), nil
	}
	got := CaptureImageDigests(context.Background(), "canton-demo")
	if got["ghcr.io/ds/canton:0.6.4"] != "sha256:zzz" {
		t.Errorf("CaptureImageDigests = %v", got)
	}

	composeImagesCmd = func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("docker daemon unreachable")
	}
	if got := CaptureImageDigests(context.Background(), "canton-demo"); got != nil {
		t.Errorf("on command error want nil, got %v", got)
	}
}
