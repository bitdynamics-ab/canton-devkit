#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
version="${VERSION:?set VERSION to the release tag}"
commit="${COMMIT:-$(git rev-parse HEAD)}"
ref="refs/tags/$version"
[[ "$version" != "dev-${commit:0:7}" ]] || ref=refs/heads/main
metadata="$(bash scripts/release-metadata.sh "$ref" "$commit")"
campaign=off
channel=dev
while IFS='=' read -r key value; do
  case "$key" in
    campaign) campaign="$value" ;;
    channel) channel="$value" ;;
  esac
done <<< "$metadata"

# A source checkout contains only a placeholder. Never ship it as the UI.
if grep -q DEVKIT_FRONTEND_PLACEHOLDER internal/ui/dist/index.html || \
   ! test -d internal/ui/dist/assets; then
  echo "Build the frontend first: make frontend" >&2
  exit 1
fi

out="${OUTPUT_DIR:-dist}"
bins="${COMPONENT_BIN_DIR:-component-bin}"
mkdir -p "$out" "$bins"
stage="$(mktemp -d "$out/.stage.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
out="$(cd "$out" && pwd)"
pkg=github.com/bitdynamics-ab/canton-devkit/internal
flags="-s -w -X main.version=$version -X main.commit=${commit:0:7} -X main.channel=$channel -X $pkg/campaign.enabled=$campaign -X $pkg/telemetry.endpoint=${TELEMETRY_ENDPOINT:-}"
read -ra targets <<< "${RELEASE_TARGETS:-linux/amd64 darwin/arm64 windows/amd64}"
archives=()
for target in "${targets[@]}"; do
  case "$target" in
    linux/amd64|darwin/arm64|windows/amd64) ;;
    *) echo "Unsupported release target: $target" >&2; exit 1 ;;
  esac
  os="${target%/*}"
  arch="${target#*/}"
  binary=canton-devkit
  [[ "$os" != windows ]] || binary=canton-devkit.exe
  bindir="$bins/${os}_${arch}"
  stagedir="$stage/${os}_${arch}"
  mkdir -p "$bindir" "$stagedir"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "$flags" -o "$bindir/$binary" ./cmd/canton-devkit
  cp "$bindir/$binary" LICENSE "$stagedir/"
  if [[ "$campaign" == on ]]; then
    cp docs/cctools-campaign.md "$stagedir/README.md"
  else
    cp README.md "$stagedir/README.md"
  fi
  archive="canton-devkit_${version}_${os}_${arch}"
  if [[ "$os" == windows ]]; then
    archive="$archive.zip"
    # zip updates an existing archive, so create a fresh one in our staging dir.
    (cd "$stagedir" && zip -q ../release.zip "$binary" LICENSE README.md)
    mv "$stage/release.zip" "$out/$archive"
  else
    archive="$archive.tar.gz"
    tar -C "$stagedir" -czf "$out/$archive" "$binary" LICENSE README.md
  fi
  archives+=("$archive")
done
printf '%s\n' "version=$version" "commit=$commit" "campaign=$campaign" \
  "channel=$channel" "targets=${targets[*]}" > "$out/BUILD-INFO.txt"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$out" && sha256sum "${archives[@]}" BUILD-INFO.txt > SHA256SUMS)
else
  (cd "$out" && shasum -a 256 "${archives[@]}" BUILD-INFO.txt > SHA256SUMS)
fi
cat "$out/BUILD-INFO.txt" "$out/SHA256SUMS"
