#!/usr/bin/env bash
# Regenerate the release-download charts/tables embedded in README.md.
#
# Fetches GitHub Releases for bitdynamics-ab/homebrew-canton-devkit (the
# repo the release workflow publishes standalone binaries/packages to —
# see release.yml's "Publish to public builds repo" step) and sums
# per-asset download_count, mirroring the logic of
# https://github.com/RamiAwar/gh-release-stats. Since GitHub README
# markdown can't execute that tool's client-side JS, this renders the
# same metric as static SVG charts + markdown tables, committed under
# docs/assets/ and refreshed by .github/workflows/release-stats.yml.
#
# Usage:
#   scripts/release-stats.sh
#
# Requires: gh (authenticated), jq.

set -euo pipefail

repo="bitdynamics-ab/homebrew-canton-devkit"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lib_dir="$root_dir/scripts/lib"
out_dir="$root_dir/docs/assets"
readme="$root_dir/README.md"

mkdir -p "$out_dir"

echo "Fetching releases for $repo..."
raw="$(gh api "repos/$repo/releases" --paginate)"

data="$(jq -s -f "$lib_dir/release-stats-process.jq" <<<"$raw")"

render_chart() {
  local mode="$1" out="$2"
  jq -nr --argjson data "$data" --arg mode "$mode" -f "$lib_dir/release-stats-charts.jq" \
    > "$out_dir/$out"
  echo "  wrote docs/assets/$out"
}

render_chart "by_platform"        "release-downloads-by-platform.svg"
render_chart "by_version"         "release-downloads-by-version.svg"
render_chart "totals_by_version"  "release-downloads-totals-by-version.svg"
render_chart "totals_by_platform" "release-downloads-totals-by-platform.svg"

jq -nr --argjson data "$data" -f "$lib_dir/release-stats-table.jq" \
  > "$out_dir/release-downloads.md"
echo "  wrote docs/assets/release-downloads.md"

# Splice the freshly rendered tables into README.md between the
# release-stats markers, so the numbers shown there stay live too.
awk -v start="<!-- release-stats:start -->" -v end="<!-- release-stats:end -->" \
    -v tablefile="$out_dir/release-downloads.md" '
  $0 == start {
    print
    while ((getline line < tablefile) > 0) print line
    close(tablefile)
    skip = 1
    next
  }
  $0 == end { skip = 0 }
  skip { next }
  { print }
' "$readme" > "$readme.tmp"
mv "$readme.tmp" "$readme"
echo "  updated README.md release-stats section"
