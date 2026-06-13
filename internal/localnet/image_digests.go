package localnet

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Image-digest capture closes the supply-chain gap the catalogue left
// open: versions.json pins the source TREE (commit SHA + post-extract
// ContentSHA), but the container images Splice's compose pulls are
// referenced by MUTABLE ghcr tags (`${IMAGE_REPO}canton:${IMAGE_TAG}`,
// etc.). A force-pushed or republished tag changes what actually runs
// with zero detection — the strongest verification covered the least
// dangerous artifact.
//
// We can't pin per-image digests via the compose env: every image in the
// stack (canton, splice-app, the web UIs) shares a single `IMAGE_TAG`
// variable, so one digest can't address six distinct images. Instead we
// VERIFY post-up: after services are healthy we ask Docker for the
// content digest (image ID) of every running Splice image and record it
// in state.json. On a later `up`/`restart` of the same version we
// compare and WARN on drift — the mitigation the integrity model was
// missing. This is a warning, not a gate: a digest can legitimately
// change if the user manually re-pulls, and bricking a healthy bring-up
// over it would be worse than the risk.

// captureImageDigestsTimeout caps the single `docker compose images`
// subprocess. One call (unlike CaptureCantonPorts' per-port loop), so a
// short timeout is enough; it rides the surrounding job timeout anyway.
const captureImageDigestsTimeout = 5 * time.Second

// composeImagesCmd is the test seam — production routes through
// exec.CommandContext; tests inject deterministic JSON.
var composeImagesCmd = func(ctx context.Context, project string) ([]byte, error) {
	cmd := exec.CommandContext(ctx,
		"docker", "compose", "-p", project, "images", "--format", "json")
	return cmd.Output()
}

// CaptureImageDigests runs `docker compose -p <project> images --format
// json` and returns a map of "<repository>:<tag>" → image content digest
// (sha256:…). Keyed by repository+tag (not container name) so the same
// image used by multiple containers (e.g. wallet-web-ui for app-provider
// AND app-user) collapses to one supply-chain identity.
//
// BEST-EFFORT, like CaptureCantonPorts: on any failure (daemon hiccup, a
// compose/Docker version whose `images` JSON we can't parse) it returns
// nil rather than failing the bring-up. A nil/empty result simply means
// "we couldn't record digests this time" — drift detection is skipped,
// never wrong.
func CaptureImageDigests(ctx context.Context, project string) map[string]string {
	subCtx, cancel := context.WithTimeout(ctx, captureImageDigestsTimeout)
	defer cancel()
	raw, err := composeImagesCmd(subCtx, project)
	if err != nil {
		return nil
	}
	digests, err := parseComposeImagesJSON(raw)
	if err != nil {
		return nil
	}
	return digests
}

// composeImage mirrors the relevant fields of one entry in `docker
// compose images --format json` output (Docker Compose v2/v5):
//
//	[{"ID":"sha256:…","ContainerName":"…","Repository":"ghcr.io/…/canton",
//	  "Tag":"0.6.4","Platform":"linux/arm64","Size":…}, …]
//
// We only need the content digest (ID) and the repo+tag identity.
type composeImage struct {
	ID         string `json:"ID"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
}

// parseComposeImagesJSON parses the `docker compose images --format json`
// payload into a repository:tag → digest map. Entries with no Repository
// (e.g. a locally-built image with no name) or no ID are skipped — they
// carry no supply-chain identity worth tracking.
//
// Pure (no I/O) so it's unit-testable without a Docker daemon, mirroring
// parseComposePortLine.
func parseComposeImagesJSON(raw []byte) (map[string]string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}
	var imgs []composeImage
	if err := json.Unmarshal([]byte(trimmed), &imgs); err != nil {
		return nil, fmt.Errorf("parse compose images json: %w", err)
	}
	out := make(map[string]string, len(imgs))
	for _, im := range imgs {
		if im.Repository == "" || im.ID == "" {
			continue
		}
		key := im.Repository + ":" + im.Tag
		out[key] = im.ID
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// DiffImageDigests returns the sorted list of "<repository>:<tag>" keys
// whose digest changed between prior and current. A key present in only
// one side is NOT reported — that's an image being added or dropped
// (e.g. an observability profile toggled on/off), not a republished tag.
// Only keys present in BOTH with a differing digest signal a mutable-tag
// republish, which is the threat worth warning about.
//
// Pure so the drift logic is unit-testable; the caller turns a non-empty
// result into a prog.Warn line.
func DiffImageDigests(prior, current map[string]string) []string {
	if len(prior) == 0 || len(current) == 0 {
		return nil
	}
	var changed []string
	for key, was := range prior {
		if now, ok := current[key]; ok && now != was {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}
