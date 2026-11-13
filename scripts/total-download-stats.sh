#!/usr/bin/env bash
#
# total-download-stats.sh — combined monthly download chart (standalone + DPM).
#
# Reads the history JSONL files produced by release-stats.sh and
# package-stats.sh, aggregates each to calendar months, sums them, and
# emits a single total monthly line chart for the README.
#
# Inputs (under OUT_DIR, optional — missing files are treated as empty):
#   release-downloads-history.jsonl
#   package-downloads-history.jsonl
#
# Outputs (into OUT_DIR):
#   total-downloads-monthly.svg — combined new downloads per month (last 6)
#   total-downloads.md          — monthly table with standalone / DPM / total
#
# Environment:
#   OUT_DIR  output directory (default: docs/assets)
#
# Requires: jq
# Reuses scripts/release-stats-monthly.jq for per-channel monthly deltas.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
OUT_DIR="${OUT_DIR:-${repo_root}/docs/assets}"

command -v jq >/dev/null || { echo "error: jq is required" >&2; exit 1; }

mkdir -p "${OUT_DIR}"

RELEASE_HISTORY="${OUT_DIR}/release-downloads-history.jsonl"
PACKAGE_HISTORY="${OUT_DIR}/package-downloads-history.jsonl"

monthly_from_history() {
  local history="$1"
  if [ ! -f "${history}" ] || [ ! -s "${history}" ]; then
    echo '[]'
    return 0
  fi
  jq -s -f "${script_dir}/release-stats-monthly.jq" "${history}"
}

standalone_monthly="$(monthly_from_history "${RELEASE_HISTORY}")"
dpm_monthly="$(monthly_from_history "${PACKAGE_HISTORY}")"

merged="$(jq -n -f "${script_dir}/total-download-stats-monthly.jq" \
  --argjson standalone "${standalone_monthly}" \
  --argjson dpm "${dpm_monthly}")"

# --- SVG helpers (single-series line chart; no legend) --------------------
svg_escape() { printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'; }

nice_max() {
  awk -v v="$1" 'BEGIN{ if (v < 5) v = 5; print (v == int(v)) ? v : int(v)+1 }'
}

# $1 = output file, $2 = title, $3 = x-axis caption,
# $4 = JSON { labels: [], series: [ {name, color, values:[]} ] }
render_line_chart() {
  local out="$1" title="$2" xlabel="$3" data="$4"

  local W=760 H=380
  local ml=56 mr=24 mt=48 mb=64
  local pw ph
  pw=$(( W - ml - mr ))
  ph=$(( H - mt - mb ))

  local n maxv step nseries
  n="$(printf '%s' "${data}" | jq '.labels | length')"
  nseries="$(printf '%s' "${data}" | jq '.series | length')"
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

    local si=0 scolor
    while [ "$si" -lt "$nseries" ]; do
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

      si=$(( si + 1 ))
    done

    printf '</svg>\n'
  } > "${out}"
  echo "wrote ${out}"
}

# Chart shows only the trailing window so x-axis labels stay readable.
by_month_data="$(printf '%s' "${merged}" | jq '
  .[-6:] as $months
  | { labels: [ $months[].month ],
      series: [ { name: "Total downloads",
                  color: "#2563eb",
                  values: [ $months[].downloads ] } ]
  }
')"

if [ "$(printf '%s' "${merged}" | jq 'length')" -eq 0 ]; then
  # Empty chart placeholder so the README image URL still resolves.
  by_month_data='{"labels":["—"],"series":[{"name":"Total downloads","color":"#2563eb","values":[0]}]}'
fi

render_line_chart "${OUT_DIR}/total-downloads-monthly.svg" \
  "Total monthly downloads" "Month (oldest -> newest)" "${by_month_data}"

# --- Markdown summary -----------------------------------------------------
{
  echo "<!-- Generated by scripts/total-download-stats.sh. Do not edit by hand. -->"
  echo "<!-- Combined from release-downloads-history.jsonl + package-downloads-history.jsonl -->"
  echo
  echo "### Total monthly downloads"
  echo
  echo "| Month | Standalone | DPM | Total |"
  echo "|---|---|---|---|"
  if [ "$(printf '%s' "${merged}" | jq 'length')" -eq 0 ]; then
    echo "| — | 0 | 0 | 0 (need history snapshots) |"
  else
    printf '%s' "${merged}" | jq -r 'reverse[] | "| \(.month) | \(.standalone) | \(.dpm) | \(.downloads) |"'
  fi
  echo
} > "${OUT_DIR}/total-downloads.md"
echo "wrote ${OUT_DIR}/total-downloads.md"
