#!/usr/bin/env bash
#
# release-stats.sh — generate GitHub release download-statistics charts.
#
# Reimplements the logic of https://github.com/RamiAwar/gh-release-stats
# (fetch GET /repos/{owner}/{repo}/releases, sum asset.download_count per
# release) as a dependency-free Bash + jq generator that emits static SVG
# charts + a markdown fragment, so the numbers can be embedded in a README
# that cannot execute JavaScript.
#
# It also appends a daily snapshot of the current totals to a history file
# so a download-over-time series accumulates from now on. The GitHub API only
# exposes CURRENT download counts (no history), so this persisted snapshot is
# the only way to build a real time series later.
#
# Outputs (into docs/assets/):
#   release-downloads-by-version.svg   — per-version total downloads over time (line)
#   release-downloads-by-platform.svg  — all-time total downloads per platform (bars, no .deb)
#   release-downloads.md               — per-version + per-platform summary tables
#   release-downloads-history.jsonl    — appended daily snapshot (total + per platform + per version)
#
# Environment:
#   STATS_REPOS   space-separated repos (default: bitdynamics-ab/canton-devkit
#                 bitdynamics-ab/homebrew-canton-devkit)
#   OUT_DIR       output directory (default: docs/assets)
#   SNAPSHOT_DATE override snapshot date, UTC YYYY-MM-DD (default: today; for testing)
#   GITHUB_TOKEN  optional; raises the GitHub API rate limit when set
#
# Requires: jq, and either `gh` or `curl`.

set -euo pipefail

STATS_REPOS="${STATS_REPOS:-bitdynamics-ab/canton-devkit bitdynamics-ab/homebrew-canton-devkit}"

# Resolve OUT_DIR relative to the repo root (parent of this script's dir),
# so the script works from any CWD.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
OUT_DIR="${OUT_DIR:-${repo_root}/docs/assets}"

command -v jq >/dev/null || { echo "error: jq is required" >&2; exit 1; }

mkdir -p "${OUT_DIR}"

# --- Fetch releases -------------------------------------------------------
# Prefer `gh` (handles auth + pagination); fall back to curl for local runs.
fetch_releases() {
  local repo="$1"
  if command -v gh >/dev/null 2>&1; then
    gh api "repos/${repo}/releases" --paginate 2>/dev/null && return 0
  fi
  local auth=()
  [ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  curl -sSfL "${auth[@]}" \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/${repo}/releases?per_page=100"
}

all_releases="[]"
for repo in ${STATS_REPOS}; do
  raw="$(fetch_releases "${repo}" || true)"
  if [ -z "${raw}" ]; then
    echo "warning: no releases returned for ${repo}" >&2
    continue
  fi
  # `gh --paginate` concatenates JSON arrays; normalise to one flat array.
  batch="$(printf '%s' "${raw}" | jq -s 'add // []')"
  count="$(printf '%s' "${batch}" | jq 'length')"
  if [ "${count}" -eq 0 ]; then
    echo "warning: no releases found for ${repo}" >&2
    continue
  fi
  echo "fetched ${count} releases from ${repo}" >&2
  all_releases="$(jq -s 'add' <<< "${all_releases}" <<< "${batch}")"
done

release_count="$(printf '%s' "${all_releases}" | jq 'length')"
if [ "${release_count}" -eq 0 ]; then
  echo "error: no releases found in any of: ${STATS_REPOS}" >&2
  exit 1
fi

# --- Classify assets into platforms + merge by tag across repos -----------
# Platform classification is regex-based (not literal filenames) because the
# asset naming drifted across historical releases. Checksum files
# (SHA256SUMS / checksums.txt) are excluded from platform totals.
#
# Releases with the same tag from multiple repos have their per-platform
# download counts summed. The earliest published_at date is kept for ordering.
model="$(printf '%s' "${all_releases}" | jq '
  def platform_of($name):
    if   ($name | test("_darwin_arm64\\."))       then "macOS (arm64)"
    elif ($name | test("_linux_amd64\\."))         then "Linux (amd64)"
    elif ($name | test("_windows_amd64\\.(zip|exe)$")) then "Windows (amd64)"
    elif ($name | test("_amd64\\.deb$"))           then "Debian (.deb)"
    elif ($name | test("(?i)^(SHA256SUMS|checksums\\.txt)$")) then null
    else null
    end;

  def normalize:
    { tag: .tag_name,
      date: (.published_at // .created_at),
      byPlatform: (
        reduce (.assets[]? | { p: platform_of(.name), dl: .download_count })
               as $a ( {};
                 if $a.p == null then . else .[$a.p] = ((.[$a.p] // 0) + $a.dl) end
               )
      )
    };

  # Fixed platform order for stable legends / stable diffs.
  ["macOS (arm64)", "Linux (amd64)", "Windows (amd64)", "Debian (.deb)"] as $platforms

  | [ .[] | normalize ]
  | group_by(.tag)
  | map(
      { tag: .[0].tag,
        date: (map(.date) | min),
        byPlatform: (
          reduce (.[] | .byPlatform | to_entries[]) as $e ( {};
            .[$e.key] = ((.[$e.key] // 0) + $e.value) )
        )
      }
      | .total = ([ .byPlatform[] ] | add // 0)
    )
  # oldest first
  | sort_by(.date)
  | { platforms: $platforms,
      releases: .,
      totalsByVersion: [ .[] | { tag, total } ],
      totalsByPlatform: (
        reduce (.[] | .byPlatform | to_entries[]) as $e ( {};
          .[$e.key] = ((.[$e.key] // 0) + $e.value) )
      ),
      grandTotal: ([ .[] | .total ] | add // 0)
    }
')"

# --- SVG helpers ----------------------------------------------------------
# Fixed, colour-blind-friendly palette; indexed to keep colours stable.
PALETTE=("#2563eb" "#16a34a" "#ea580c" "#9333ea" "#0891b2" "#ca8a04")

svg_escape() { printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'; }

# Round a float up to a "nice" axis maximum (>= 5, ceil).
nice_max() {
  awk -v v="$1" 'BEGIN{ if (v < 5) v = 5; print (v == int(v)) ? v : int(v)+1 }'
}

# ------------------------------------------------------------------------
# Line chart renderer.
#   $1 = output file
#   $2 = chart title
#   $3 = JSON: { labels: [..], series: [ {name, color, values:[..]} ] }
# ------------------------------------------------------------------------
render_line_chart() {
  local out="$1" title="$2" data="$3"

  local W=760 H=380
  local ml=56 mr=180 mt=48 mb=64        # margins (mr wide for legend)
  local pw ph
  pw=$(( W - ml - mr ))
  ph=$(( H - mt - mb ))

  local labels n maxv
  labels="$(printf '%s' "${data}" | jq -r '.labels | join("\u0001")')"
  n="$(printf '%s' "${data}" | jq '.labels | length')"
  maxv="$(printf '%s' "${data}" | jq '[.series[].values[]] | max // 0')"
  maxv="$(nice_max "${maxv}")"

  {
    printf '<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="Segoe UI, Helvetica, Arial, sans-serif" role="img" aria-label="%s">\n' \
      "$W" "$H" "$W" "$H" "$(svg_escape "${title}")"
    printf '<rect width="%d" height="%d" fill="#ffffff"/>\n' "$W" "$H"
    printf '<text x="%d" y="26" font-size="16" font-weight="600" fill="#111827">%s</text>\n' "$ml" "$(svg_escape "${title}")"

    # Y grid + labels (5 ticks)
    local i gy val
    for i in 0 1 2 3 4 5; do
      gy=$(awk -v mt="$mt" -v ph="$ph" -v i="$i" 'BEGIN{printf "%.1f", mt + ph - (ph*i/5)}')
      val=$(awk -v m="$maxv" -v i="$i" 'BEGIN{printf "%d", m*i/5}')
      printf '<line x1="%d" y1="%s" x2="%d" y2="%s" stroke="#e5e7eb" stroke-width="1"/>\n' \
        "$ml" "$gy" $(( ml + pw )) "$gy"
      printf '<text x="%d" y="%s" font-size="11" fill="#6b7280" text-anchor="end">%s</text>\n' \
        $(( ml - 8 )) "$(awk -v g="$gy" 'BEGIN{printf "%.1f", g+4}')" "$val"
    done

    # Axes
    printf '<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#9ca3af" stroke-width="1"/>\n' \
      "$ml" "$mt" "$ml" $(( mt + ph ))
    printf '<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#9ca3af" stroke-width="1"/>\n' \
      "$ml" $(( mt + ph )) $(( ml + pw )) $(( mt + ph ))

    # X labels
    local idx=0 lbl xx
    IFS=$'\001' read -ra _labels <<< "${labels}"
    for lbl in "${_labels[@]}"; do
      if [ "$n" -gt 1 ]; then
        xx=$(awk -v ml="$ml" -v pw="$pw" -v i="$idx" -v n="$n" 'BEGIN{printf "%.1f", ml + pw*i/(n-1)}')
      else
        xx=$(awk -v ml="$ml" -v pw="$pw" 'BEGIN{printf "%.1f", ml + pw/2}')
      fi
      printf '<text x="%s" y="%d" font-size="11" fill="#6b7280" text-anchor="middle">%s</text>\n' \
        "$xx" $(( mt + ph + 20 )) "$(svg_escape "${lbl}")"
      idx=$(( idx + 1 ))
    done
    printf '<text x="%d" y="%d" font-size="12" fill="#374151" text-anchor="middle">Release tag (oldest -> newest)</text>\n' \
      $(( ml + pw/2 )) $(( H - 12 ))

    # Series: polylines + points
    local si=0 sname scolor
    while IFS= read -r sname; do
      scolor="$(printf '%s' "${data}" | jq -r --argjson i "$si" '.series[$i].color')"
      # polyline points
      local pts=""
      local vi=0 v px py
      while IFS= read -r v; do
        if [ "$n" -gt 1 ]; then
          px=$(awk -v ml="$ml" -v pw="$pw" -v i="$vi" -v n="$n" 'BEGIN{printf "%.1f", ml + pw*i/(n-1)}')
        else
          px=$(awk -v ml="$ml" -v pw="$pw" 'BEGIN{printf "%.1f", ml + pw/2}')
        fi
        py=$(awk -v mt="$mt" -v ph="$ph" -v val="$v" -v m="$maxv" 'BEGIN{printf "%.1f", mt + ph - (ph*val/m)}')
        pts="${pts} ${px},${py}"
        vi=$(( vi + 1 ))
      done < <(printf '%s' "${data}" | jq -r --argjson i "$si" '.series[$i].values[]')

      printf '<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5"/>\n' "${pts# }" "${scolor}"
      # points
      for p in ${pts}; do
        printf '<circle cx="%s" cy="%s" r="3.5" fill="%s"/>\n' "${p%,*}" "${p#*,}" "${scolor}"
      done

      # legend entry
      local ly=$(( mt + si*22 ))
      printf '<rect x="%d" y="%d" width="14" height="14" rx="2" fill="%s"/>\n' \
        $(( ml + pw + 16 )) "$ly" "${scolor}"
      printf '<text x="%d" y="%d" font-size="12" fill="#374151">%s</text>\n' \
        $(( ml + pw + 36 )) $(( ly + 12 )) "$(svg_escape "${sname}")"

      si=$(( si + 1 ))
    done < <(printf '%s' "${data}" | jq -r '.series[].name')

    printf '</svg>\n'
  } > "${out}"
  echo "wrote ${out}"
}

# ------------------------------------------------------------------------
# Horizontal bar chart renderer.
#   $1 = output file
#   $2 = chart title
#   $3 = JSON: { bars: [ {label, value} ] }
# ------------------------------------------------------------------------
render_bar_chart() {
  local out="$1" title="$2" data="$3"

  local nbars maxv
  nbars="$(printf '%s' "${data}" | jq '.bars | length')"
  maxv="$(printf '%s' "${data}" | jq '[.bars[].value] | max // 0')"
  maxv="$(nice_max "${maxv}")"

  local ml=140 mr=48 mt=48 mb=24
  local rowh=30 gap=10
  local pw=440
  local ph=$(( nbars * (rowh + gap) ))
  local W=$(( ml + pw + mr ))
  local H=$(( mt + ph + mb ))

  {
    printf '<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="Segoe UI, Helvetica, Arial, sans-serif" role="img" aria-label="%s">\n' \
      "$W" "$H" "$W" "$H" "$(svg_escape "${title}")"
    printf '<rect width="%d" height="%d" fill="#ffffff"/>\n' "$W" "$H"
    printf '<text x="16" y="26" font-size="16" font-weight="600" fill="#111827">%s</text>\n' "$(svg_escape "${title}")"

    local i=0 label value y bw color
    while IFS= read -r label; do
      value="$(printf '%s' "${data}" | jq -r --argjson i "$i" '.bars[$i].value')"
      y=$(( mt + i*(rowh+gap) ))
      bw=$(awk -v pw="$pw" -v v="$value" -v m="$maxv" 'BEGIN{printf "%.1f", (m>0)?pw*v/m:0}')
      color="${PALETTE[$(( i % ${#PALETTE[@]} ))]}"
      printf '<text x="%d" y="%d" font-size="12" fill="#374151" text-anchor="end">%s</text>\n' \
        $(( ml - 10 )) $(( y + rowh/2 + 4 )) "$(svg_escape "${label}")"
      printf '<rect x="%d" y="%d" width="%s" height="%d" rx="3" fill="%s"/>\n' \
        "$ml" "$y" "$bw" "$rowh" "$color"
      printf '<text x="%s" y="%d" font-size="12" font-weight="600" fill="#374151">%s</text>\n' \
        "$(awk -v ml="$ml" -v bw="$bw" 'BEGIN{printf "%.1f", ml+bw+6}')" $(( y + rowh/2 + 4 )) "$value"
      i=$(( i + 1 ))
    done < <(printf '%s' "${data}" | jq -r '.bars[].label')

    printf '</svg>\n'
  } > "${out}"
  echo "wrote ${out}"
}

# --- View 1: per-version total downloads over time ------------------------
by_version_data="$(printf '%s' "${model}" | jq '
  { labels: [ .releases[].tag ],
    series: [ { name: "Total downloads",
                color: "#2563eb",
                values: [ .releases[].total ] } ]
  }
')"
render_line_chart "${OUT_DIR}/release-downloads-by-version.svg" \
  "Total downloads, by release" "${by_version_data}"

# --- View 2: all-time total downloads per platform (bars, no .deb) -------
platform_bars="$(printf '%s' "${model}" | jq '
  { bars: [ .platforms[] as $p | select($p != "Debian (.deb)") | { label: $p, value: (.totalsByPlatform[$p] // 0) } ] }
')"
render_bar_chart "${OUT_DIR}/release-downloads-by-platform.svg" \
  "All-time downloads per platform" "${platform_bars}"

# --- Markdown summary tables ---------------------------------------------
{
  echo "<!-- Generated by scripts/release-stats.sh. Do not edit by hand. -->"
  echo "<!-- Source repos: ${STATS_REPOS} -->"
  echo
  printf '**Total downloads:** %s across %s releases.\n\n' \
    "$(printf '%s' "${model}" | jq -r '.grandTotal')" \
    "$(printf '%s' "${model}" | jq -r '.releases | length')"

  echo "### Downloads per version"
  echo
  echo "| Version | Downloads |"
  echo "|---|---|"
  printf '%s' "${model}" | jq -r '
    (.totalsByVersion | reverse)[] | "| \(.tag) | \(.total) |"'
  echo
  echo "### Downloads per platform"
  echo
  echo "| Platform | Downloads |"
  echo "|---|---|"
  printf '%s' "${model}" | jq -r '
    .platforms[] as $p | select($p != "Debian (.deb)") | "| \($p) | \(.totalsByPlatform[$p] // 0) |"'
  echo
} > "${OUT_DIR}/release-downloads.md"
echo "wrote ${OUT_DIR}/release-downloads.md"

# --- Append today's snapshot to the history file --------------------------
# One row per UTC day: { date, total, byPlatform, byVersion }. Re-running on
# the same day replaces that day's row (idempotent), so the file stays clean
# and ordered. This accumulates the time series the GitHub API can't provide.
HISTORY_FILE="${OUT_DIR}/release-downloads-history.jsonl"
snapshot_date="${SNAPSHOT_DATE:-$(date -u +%Y-%m-%d)}"

today_row="$(printf '%s' "${model}" | jq -c --arg d "${snapshot_date}" '
  { date: $d,
    total: .grandTotal,
    byPlatform: .totalsByPlatform,
    byVersion: ( [ .totalsByVersion[] | { (.tag): .total } ] | add // {} ) }')"

touch "${HISTORY_FILE}"
{
  jq -c --arg d "${snapshot_date}" 'select(.date != $d)' "${HISTORY_FILE}" 2>/dev/null || true
  printf '%s\n' "${today_row}"
} | jq -s -c 'sort_by(.date) | .[]' > "${HISTORY_FILE}.tmp"
mv "${HISTORY_FILE}.tmp" "${HISTORY_FILE}"
echo "updated ${HISTORY_FILE} (snapshot ${snapshot_date})"
