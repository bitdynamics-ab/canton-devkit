package localnet

import (
	"fmt"
	"io"
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
// Idempotent and edit-preserving. The overlay is written only when the
// destination is missing or still byte-identical to the embedded
// asset; once an operator has tweaked it in place (e.g. to point at a
// different snapshot), MaterializeTokensV2Overlay leaves their version
// untouched and emits a one-line drift notice on warnw so the
// divergence isn't silent. This matches the doc promise that operator
// edits survive a `localnet up`.
//
// warnw may be nil (drift notices are then dropped).
//
// dataDir is the per-instance data directory (registry's DataDir);
// the overlay lands under `<dataDir>/tokens-v2/`. The tokens-v2
// overlay declares no relative volume mounts, so — unlike the
// observability overlay — it needs nothing in the splice-base project
// directory.
func MaterializeTokensV2Overlay(dataDir string, warnw io.Writer) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("MaterializeTokensV2Overlay: empty dataDir")
	}
	root := filepath.Join(dataDir, "tokens-v2")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create tokens-v2 dir: %w", err)
	}

	const embedPath = "compose/tokens-v2.yml"
	// assets.FS is the shared compose+grafana embed; the tokens-v2
	// overlay lives under compose/ in that tree.
	data, err := fs.ReadFile(assets.FS, embedPath)
	if err != nil {
		return "", fmt.Errorf("read embedded %s: %w", embedPath, err)
	}
	dest := filepath.Join(root, "tokens-v2.yml")
	if err := writePreservingEdits(dest, data, warnw); err != nil {
		return "", fmt.Errorf("write tokens-v2 overlay: %w", err)
	}
	return dest, nil
}
