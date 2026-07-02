#!/usr/bin/env bash
# Assemble per-platform DPM component directories for `dpm publish
# component`.
#
# A DPM component is an OCI artifact containing a binary + a component.yaml
# manifest. For a multi-platform publish, each platform gets its own
# directory (binary + manifest), and `dpm publish component` is given one
# --platform=<os>/<arch>=<dir> per target. This script builds those
# directories from the repo's component.yaml + cross-compiled binaries.
#
# Usage:
#   scripts/assemble-dpm-component.sh <version> [out-dir]
#
# Produces (default out-dir: dist/component):
#   dist/component/linux_amd64/{canton-devkit, component.yaml}
#   dist/component/darwin_arm64/{canton-devkit, component.yaml}
#   dist/component/windows_amd64/{canton-devkit.exe, component.yaml}
#
# Then publish (needs dpm + registry auth):
#   dpm publish component oci://<registry>/canton-devkit:<version> \
#     --platform linux/amd64=dist/component/linux_amd64 \
#     --platform darwin/arm64=dist/component/darwin_arm64 \
#     --platform windows/amd64=dist/component/windows_amd64

set -euo pipefail

VERSION="${1:?usage: assemble-dpm-component.sh <version> [out-dir]}"
OUT="${2:-dist/component}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMMIT="${COMMIT:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || true)}"
MANIFEST="$ROOT/component.yaml"

# os/arch → dpm platform tuple. Mirrors the release matrix.
TARGETS="linux/amd64 darwin/arm64 windows/amd64"

rm -rf "$OUT"
for target in $TARGETS; do
  os="${target%/*}"; arch="${target#*/}"
  dir="$OUT/${os}_${arch}"
  mkdir -p "$dir"

  bin="canton-devkit"
  [ "$os" = "windows" ] && bin="canton-devkit.exe"

  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o "$dir/$bin" "$ROOT/cmd/canton-devkit"

  # Manifest: rewrite the binary path to the platform's filename
  # (canton-devkit.exe on Windows). Matches the `path: canton-devkit`
  # list-item line regardless of leading whitespace/dash.
  sed "s|path: canton-devkit$|path: ${bin}|" "$MANIFEST" > "$dir/component.yaml"

  echo "assembled $target → $dir ($(du -h "$dir/$bin" | cut -f1))"
done

echo "DPM component dirs ready under $OUT (version $VERSION)"
