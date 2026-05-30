package localnet

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bitdynamics-ab/canton-devkit/assets"
)

// TokensV2ProfileName is the docker-compose profile that enables the
// Token Standard V2 alpha-protocol Canton config overlay. The overlay
// file itself (assets/compose/tokens-v2.yml) carries no service-level
// `profiles:` key — it only injects env into the always-on canton
// service — so the actual gate is that we only append the overlay file
// to ComposeRunner.ComposeFiles when this profile is on. See the
// comment in tokens-v2.yml for why we don't use compose-level profile
// gating here (it would gate canton itself).
const TokensV2ProfileName = "tokens-v2"

// MaterializeTokensV2Overlay writes the embedded tokens-v2 compose
// overlay into a per-instance directory and returns the absolute path
// of the overlay YAML to append to ComposeRunner.ComposeFiles.
//
// Idempotent. Mirrors MaterializeObservabilityOverlay's hash-compare
// write-if-different pattern so operators who tweak the overlay in
// place (e.g. to point at a different snapshot) don't have their
// edits clobbered on every `localnet up`.
//
// dataDir is the per-instance data directory (registry's DataDir);
// the overlay lands under `<dataDir>/tokens-v2/`. projectDir is
// accepted for symmetry with the observability overlay but is unused
// — the tokens-v2 overlay declares no relative volume mounts, so it
// needs nothing in the splice-base project directory.
func MaterializeTokensV2Overlay(dataDir string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("MaterializeTokensV2Overlay: empty dataDir")
	}
	root := filepath.Join(dataDir, "tokens-v2")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create tokens-v2 dir: %w", err)
	}

	const embedPath = "compose/tokens-v2.yml"
	data, err := fs.ReadFile(assets.Observability, embedPath)
	if err != nil {
		return "", fmt.Errorf("read embedded %s: %w", embedPath, err)
	}
	dest := filepath.Join(root, "tokens-v2.yml")
	// Write-if-different — don't silently stomp operator tweaks.
	if existing, rerr := os.ReadFile(dest); rerr == nil && bytesEqual(existing, data) {
		return dest, nil
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("write tokens-v2 overlay: %w", err)
	}
	return dest, nil
}
