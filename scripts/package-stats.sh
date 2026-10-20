#!/usr/bin/env bash
#
# package-stats.sh — generate GHCR package download-statistics charts.
#
# Scrapes the public GitHub Packages container page for canton-devkit (no
# read:packages token required), parses the cumulative Total downloads figure
# and the last-30-day sparkline, and emits static SVG + markdown for the
# README. Separate from scripts/release-stats.sh (standalone release assets).
#
# It appends a daily snapshot to a history file so a download-over-time series
# accumulates. On first run (empty/short history), the sparkline is used to
# backfill prior end-of-day cumulative totals.
#
# Data source (default):
#   https://github.com/bitdynamics-ab/canton-devkit/pkgs/container/canton-devkit
#
# Outputs (into docs/assets/):
#   package-downloads-monthly.svg   — new downloads per calendar month (line; last 6 months)
#   package-downloads.md            — total + monthly summary table
#   package-downloads-history.jsonl — appended daily snapshot (total + last30Days)
#
# Environment:
#   PACKAGE_URL        package page URL (default: bitdynamics-ab canton-devkit container)
#   PACKAGE_HTML_FILE  if set, read HTML from this file instead of curling PACKAGE_URL
#   OUT_DIR            output directory (default: docs/assets)
#   SNAPSHOT_DATE      override snapshot date, UTC YYYY-MM-DD (default: today; for testing)
#
# Requires: jq, curl (unless PACKAGE_HTML_FILE is set).

set -euo pipefail

PACKAGE_URL="${PACKAGE_URL:-https://github.com/bitdynamics-ab/canton-devkit/pkgs/container/canton-devkit}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
OUT_DIR="${OUT_DIR:-${repo_root}/docs/assets}"

command -v jq >/dev/null || { echo "error: jq is required" >&2; exit 1; }

mkdir -p "${OUT_DIR}"

# --- Fetch package HTML ---------------------------------------------------
fetch_html() {
  if [ -n "${PACKAGE_HTML_FILE:-}" ]; then
    cat "${PACKAGE_HTML_FILE}"
    return 0
  fi
  command -v curl >/dev/null || { echo "error: curl is required" >&2; exit 1; }
  curl -sSfL \
    -H "Accept: text/html" \
    -A "canton-devkit-package-stats/1.0" \
    "${PACKAGE_URL}"
}

html="$(fetch_html)"
if [ -z "${html}" ]; then
  echo "error: empty package page from ${PACKAGE_URL}" >&2
  exit 1
fi

# --- Parse total + sparkline ----------------------------------------------
# Total downloads lives in: <h3 title="1287">1.29K</h3> under "Total downloads".
# jq 1.6 scan() returns positional capture arrays, not named objects.
parsed="$(printf '%s' "${html}" | jq -Rs '
  . as $html
  | ($html | capture("Total downloads</span>\\s*<h3 title=\"(?<total>[0-9]+)\">"; "m") // null) as $t
  | if $t == null then error("could not parse Total downloads from package page") else . end
  | ($html
      | [scan("data-merge-count=\"([0-9]+)\"\\s*data-date=\"([0-9]{4}-[0-9]{2}-[0-9]{2})\"")]
      | map({count: (.[0] | tonumber), date: .[1]})) as $spark_a
  | ($html
      | [scan("data-date=\"([0-9]{4}-[0-9]{2}-[0-9]{2})\"[\\s\\S]{0,200}?data-merge-count=\"([0-9]+)\"")]
      | map({date: .[0], count: (.[1] | tonumber)})) as $spark_b
  | (if ($spark_a | length) > 0 then $spark_a else $spark_b end) as $spark
  | {
      total: ($t.total | tonumber),
      sparkline: ($spark | sort_by(.date)
        | group_by(.date)
        | map({date: .[0].date, count: (map(.count) | max)})),
      last30Days: (($spark | map(.count) | add) // 0)
    }
')"

total="$(printf '%s' "${parsed}" | jq -r '.total')"
last30="$(printf '%s' "${parsed}" | jq -r '.last30Days')"
echo "parsed total=${total} last30Days=${last30}" >&2

# --- SVG helpers (line chart only) ----------------------------------------
svg_escape() { printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'; }

nice_max() {
  awk -v v="$1" 'BEGIN{ if (v < 5) v = 5; print (v == int(v)) ? v : int(v)+1 }'
}

# $1 = output file, $2 = title, $3 = x-axis caption,
# $4 = JSON { labels: [], series: [ {name, color, values:[]} ] }
render_line_chart() {
  local out="$1" title="$2" xlabel="$3" data="$4"

  local W=760 H=380
  local ml=56 mr=180 mt=48 mb=64
  local pw ph
  pw=$(( W - ml - mr ))
  ph=$(( H - mt - mb ))

  local n maxv step
  n="$(printf '%s' "${data}" | jq '.labels | length')"
  maxv="$(printf '%s' "${data}" | jq '[.series[].values[]] | max // 0')"
  maxv="$(nice_max "${maxv}")"
  step=$(awk -v n="$n" 'BEGIN{ if (n <= 12) print 1; else print int((n + 11) / 12) }')

  {
    printf '<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="Segoe UI, Helvetica, Arial, sans-serif" role="img" aria-label="%s">\n' \
      "$W" "$H" "$W" "$H" "$(svg_escape "${title}")"
    printf '<rect width="%d" height="%d" fill="#ffffff"/>\n' "$W" "$H"
    printf '<text x="%d" y="26" font-size="16" font-weight="600" fill="#111827">%s</text>\n' "$ml" "$(svg_escape "${title}")"

    local i gy val
    for i in 0 1 2 3 4 5; do
      gy=$(awk -v mt="$mt" -v ph="$ph" -v i="$i" 'BEGIN{printf "%.1f", mt + ph - (ph*i/5)}')
      val=$(awk -v m="$maxv" -v i="$i" 'BEGIN{printf "%d", m*i/5}')
      printf '<line x1="%d" y1="%s" x2="%d" y2="%s" stroke="#e5e7eb" stroke-width="1"/>\n' \
        "$ml" "$gy" $(( ml + pw )) "$gy"
      printf '<text x="%d" y="%s" font-size="11" fill="#6b7280" text-anchor="end">%s</text>\n' \
        $(( ml - 8 )) "$(awk -v g="$gy" 'BEGIN{printf "%.1f", g+4}')" "$val"
    done

    printf '<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#9ca3af" stroke-width="1"/>\n' \
      "$ml" "$mt" "$ml" $(( mt + ph ))
    printf '<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#9ca3af" stroke-width="1"/>\n' \
      "$ml" $(( mt + ph )) $(( ml + pw )) $(( mt + ph ))

    local idx=0 lbl xx
    while IFS= read -r lbl; do
      if [ "$n" -gt 1 ]; then
        xx=$(awk -v ml="$ml" -v pw="$pw" -v i="$idx" -v n="$n" 'BEGIN{printf "%.1f", ml + pw*i/(n-1)}')
      else
        xx=$(awk -v ml="$ml" -v pw="$pw" 'BEGIN{printf "%.1f", ml + pw/2}')
      fi
      if [ $(( idx % step )) -eq 0 ] || [ "$idx" -eq $(( n - 1 )) ]; then
        printf '<text x="%s" y="%d" font-size="11" fill="#6b7280" text-anchor="middle">%s</text>\n' \
          "$xx" $(( mt + ph + 20 )) "$(svg_escape "${lbl}")"
      fi
      idx=$(( idx + 1 ))
    done < <(printf '%s' "${data}" | jq -r '.labels[]')
    printf '<text x="%d" y="%d" font-size="12" fill="#374151" text-anchor="middle">%s</text>\n' \
      $(( ml + pw/2 )) $(( H - 12 )) "$(svg_escape "${xlabel}")"

    local si=0 sname scolor
    while IFS= read -r sname; do
      scolor="$(printf '%s' "${data}" | jq -r --argjson i "$si" '.series[$i].color')"
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
      for p in ${pts}; do
        printf '<circle cx="%s" cy="%s" r="3.5" fill="%s"/>\n' "${p%,*}" "${p#*,}" "${scolor}"
      done

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

# --- History: seed from sparkline if empty, then upsert today -------------
HISTORY_FILE="${OUT_DIR}/package-downloads-history.jsonl"
snapshot_date="${SNAPSHOT_DATE:-$(date -u +%Y-%m-%d)}"

touch "${HISTORY_FILE}"
history_len="$(jq -s 'length' "${HISTORY_FILE}" 2>/dev/null || echo 0)"

# Backfill when history has fewer than 2 rows so monthly deltas are possible
# immediately from the page sparkline.
if [ "${history_len}" -lt 2 ]; then
  echo "seeding history from sparkline (history_len=${history_len})" >&2
  spark_seed="$(printf '%s' "${parsed}" | jq -c --arg d "${snapshot_date}" --argjson total "${total}" --argjson last30 "${last30}" '
    .sparkline as $spark
    | ($spark | map(.count) | add // 0) as $sum
    | ([$total - $sum, 0] | max) as $start
    | reduce $spark[] as $day (
        { running: $start, rows: [] };
        .running += $day.count
        | .rows += [{ date: $day.date, total: .running, last30Days: null }]
      )
    | .rows as $rows
    | if ($rows | map(.date) | index($d)) then $rows
      else $rows + [{ date: $d, total: $total, last30Days: $last30 }]
      end
    | map(if .date == $d then .last30Days = $last30 else . end)
  ')"
  printf '%s' "${spark_seed}" | jq -c '.[]' > "${HISTORY_FILE}.tmp"
  mv "${HISTORY_FILE}.tmp" "${HISTORY_FILE}"
fi

today_row="$(jq -nc --arg d "${snapshot_date}" --argjson total "${total}" --argjson last30 "${last30}" \
  '{date: $d, total: $total, last30Days: $last30}')"

{
  jq -c --arg d "${snapshot_date}" 'select(.date != $d)' "${HISTORY_FILE}" 2>/dev/null || true
  printf '%s\n' "${today_row}"
} | jq -s -c 'sort_by(.date) | .[]' > "${HISTORY_FILE}.tmp"
mv "${HISTORY_FILE}.tmp" "${HISTORY_FILE}"
echo "updated ${HISTORY_FILE} (snapshot ${snapshot_date})"

# --- Monthly chart from history deltas ------------------------------------
monthly_rows="$(jq -s -f "${script_dir}/package-stats-monthly.jq" "${HISTORY_FILE}")"

by_month_data="$(printf '%s' "${monthly_rows}" | jq '
  .[-6:] as $months
  | { labels: [ $months[].month ],
      series: [ { name: "Downloads",
                  color: "#2563eb",
                  values: [ $months[].downloads ] } ]
  }
')"
render_line_chart "${OUT_DIR}/package-downloads-monthly.svg" \
  "Monthly DPM package downloads (last 6 months)" "Month (oldest -> newest)" "${by_month_data}"

# --- Markdown summary -----------------------------------------------------
{
  echo "<!-- Generated by scripts/package-stats.sh. Do not edit by hand. -->"
  echo "<!-- Source: ${PACKAGE_URL} -->"
  echo
  printf '**Total downloads:** %s (GHCR package `canton-devkit`).\n\n' "${total}"
  printf '**Last 30 days (page sparkline):** %s.\n\n' "${last30}"
  echo "Source: [GitHub Packages](${PACKAGE_URL})."
  echo
  echo "### Monthly downloads"
  echo
  echo "| Month | Downloads |"
  echo "|---|---|"
  if [ "$(printf '%s' "${monthly_rows}" | jq 'length')" -eq 0 ]; then
    echo "| — | 0 (need at least two daily snapshots) |"
  else
    printf '%s' "${monthly_rows}" | jq -r 'reverse[] | "| \(.month) | \(.downloads) |"'
  fi
  echo
} > "${OUT_DIR}/package-downloads.md"
echo "wrote ${OUT_DIR}/package-downloads.md"
