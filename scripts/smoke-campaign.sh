#!/usr/bin/env bash
set -euo pipefail

binary="${1:?expected the native campaign binary}"
version="${2:?expected the release version}"
commit="${3:?expected the short commit SHA}"
tmp="$(mktemp -d)"
pid=
cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT
unset DEVKIT_CAMPAIGN
export DEVKIT_CAMPAIGN_FILE="$tmp/campaign-id"
export CANTON_DEVKIT_REGISTRY="$tmp/registry"
export CANTON_DEVKIT_TELEMETRY_DIR="$tmp/telemetry" DO_NOT_TRACK=1

first="$("$binary" version)"
[[ "$first" == "canton-devkit $version ($commit)"$'\n'"campaign code: CCT-"* ]]
code="$(cat "$DEVKIT_CAMPAIGN_FILE")"
[[ "$code" =~ ^CCT-[A-Z2-7]{8}$ ]]
[[ "$("$binary" version)" == "$first" ]]
[[ "$("$binary" --version)" == *"campaign code: $code" ]]
[[ "$(DEVKIT_CAMPAIGN=0 "$binary" version)" == "canton-devkit $version ($commit)" ]]

"$binary" localnet ui --port 0 > "$tmp/ui.log" 2>&1 &
pid=$!
url=
for ((attempt=0; attempt<100; attempt++)); do
  url="$(grep -Eo 'http://127\.0\.0\.1:[0-9]+/' "$tmp/ui.log" | head -1 || true)"
  if [[ -n "$url" ]] && curl -fsS --max-time 2 "${url}healthz" >/dev/null; then
    break
  fi
  kill -0 "$pid" 2>/dev/null || { cat "$tmp/ui.log" >&2; exit 1; }
  sleep 0.1
done
[[ -n "$url" ]] || { cat "$tmp/ui.log" >&2; exit 1; }
curl -fsS --max-time 5 "${url}api/version" > "$tmp/version.json"
curl -fsS --max-time 5 "$url" > "$tmp/index.html"
# Node is already required for the release's frontend build.
node --input-type=module - "$tmp" "$version" "$commit" "$code" "$url" <<'JS'
import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
const [dir, version, commit, code, url] = process.argv.slice(2);
const info = JSON.parse(readFileSync(`${dir}/version.json`, 'utf8'));
assert.equal(info.version, `${version} (${commit})`);
assert.equal(info.campaign_code, code);
assert.equal(info.cli_prefix, 'canton-devkit localnet');
const html = readFileSync(`${dir}/index.html`, 'utf8');
assert(!html.includes('DEVKIT_FRONTEND_PLACEHOLDER'));
const assets = [...html.matchAll(/(?:src|href)="(\/assets\/[^\"]+)"/g)].map(m => m[1]);
assert(assets.some(path => path.endsWith('.js')), 'missing JavaScript bundle');
assert(assets.some(path => path.endsWith('.css')), 'missing CSS bundle');
for (const path of assets) {
  const response = await fetch(new URL(path, url), { signal: AbortSignal.timeout(5000) });
  assert.equal(response.status, 200, path);
  assert(!response.headers.get('content-type')?.includes('text/html'), path);
  assert((await response.arrayBuffer()).byteLength > 0, path);
}
JS
printf 'PASS: %s (%s), persistent campaign code, opt-out, version API, embedded JS/CSS\n' "$version" "$commit"
