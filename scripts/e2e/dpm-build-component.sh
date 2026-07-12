#!/usr/bin/env bash
# Assemble a local DevKit component directory the dpm localnet E2E tests
# can install via a file-based component reference ({name, path}).
#
# Layout mirrors docs/packaging.md: the built binary under bin/ plus the
# component.yaml rendered from packaging/component.yaml.tmpl. Resolving
# from a local path keeps the E2E fully hermetic (no OCI registry, no
# TLS) and exercises the binary built in THIS run.
#
# Usage:
#   scripts/e2e/dpm-build-component.sh [OUT_DIR]
# Prints the absolute component dir on stdout. OUT_DIR defaults to
# .tmp/e2e-dpm-component under the repo root (per AGENTS.md: no /tmp).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${1:-${ROOT_DIR}/.tmp/e2e-dpm-component}"
BIN="${CDK_BIN:-${ROOT_DIR}/bin/canton-devkit}"

if [ ! -x "$BIN" ]; then
  echo "binary not found at $BIN -- run 'make build' first" >&2
  exit 1
fi

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR/bin"
cp "$BIN" "$OUT_DIR/bin/canton-devkit"
sed "s|@@BINARY_PATH@@|bin/canton-devkit|" \
  "${ROOT_DIR}/packaging/component.yaml.tmpl" > "$OUT_DIR/component.yaml"
cp "${ROOT_DIR}/LICENSE" "$OUT_DIR/LICENSE"

( cd "$OUT_DIR" && pwd )
