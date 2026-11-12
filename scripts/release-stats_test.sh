#!/usr/bin/env bash
# Regression checks for monthly aggregation in scripts/release-stats-monthly.jq.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
jq_filter="${script_dir}/release-stats-monthly.jq"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

assert_eq() {
  local name="$1" got="$2" exp="$3"
  got="$(printf '%s' "${got}" | jq -c .)"
  exp="$(printf '%s' "${exp}" | jq -c .)"
  if [ "${got}" != "${exp}" ]; then
    echo "FAIL: ${name}" >&2
    echo "  got:      ${got}" >&2
    echo "  expected: ${exp}" >&2
    exit 1
  fi
}

# Same-month deltas sum; baseline snapshot excluded.
cat > "${tmp}/history.jsonl" <<'EOF'
{"date":"2026-07-06","total":10}
{"date":"2026-07-20","total":12}
{"date":"2026-07-28","total":15}
{"date":"2026-08-05","total":18}
EOF
assert_eq "monthly aggregation" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history.jsonl")" \
  '[{"month":"2026-07","downloads":5},{"month":"2026-08","downloads":3}]'

# Gap fill: July and September observed → August appears as 0.
cat > "${tmp}/history-gap.jsonl" <<'EOF'
{"date":"2026-07-06","total":10}
{"date":"2026-07-10","total":14}
{"date":"2026-09-02","total":20}
EOF
assert_eq "gap fill" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-gap.jsonl")" \
  '[{"month":"2026-07","downloads":4},{"month":"2026-08","downloads":0},{"month":"2026-09","downloads":6}]'

# Negative cumulative correction clamps that step to zero.
cat > "${tmp}/history-clamp.jsonl" <<'EOF'
{"date":"2026-08-03","total":20}
{"date":"2026-08-04","total":17}
{"date":"2026-08-05","total":19}
EOF
assert_eq "negative delta clamp" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-clamp.jsonl")" \
  '[{"month":"2026-08","downloads":2}]'

# Single snapshot → no monthly points yet.
printf '%s\n' '{"date":"2026-08-03","total":20}' > "${tmp}/history-one.jsonl"
assert_eq "single snapshot" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-one.jsonl")" \
  '[]'

# Chart window: last 6 months only (full series may be longer).
window="$(printf '%s' '[{"month":"M1","downloads":1},{"month":"M2","downloads":2},{"month":"M3","downloads":3},{"month":"M4","downloads":4},{"month":"M5","downloads":5},{"month":"M6","downloads":6},{"month":"M7","downloads":7},{"month":"M8","downloads":8}]' | jq -c '.[-6:] | { labels: [.[].month], values: [.[].downloads] }')"
assert_eq "chart last-6 window" \
  "${window}" \
  '{"labels":["M3","M4","M5","M6","M7","M8"],"values":[3,4,5,6,7,8]}'

bash -n "${script_dir}/release-stats.sh"

# Checksums count toward totals (badge / grandTotal), not toward null.
assert_eq "checksum classification" \
  "$(jq -n '
    def platform_of($name):
      if   ($name | test("_darwin_arm64\\.")) then "macOS (arm64)"
      elif ($name | test("_linux_amd64\\.")) then "Linux (amd64)"
      elif ($name | test("_windows_amd64\\.(zip|exe)$")) then "Windows (amd64)"
      elif ($name | test("_amd64\\.deb$")) then "Debian (.deb)"
      elif ($name | test("(?i)^(SHA256SUMS|checksums\\.txt)$")) then "Checksums"
      else null end;
    [platform_of("SHA256SUMS"), platform_of("checksums.txt"), platform_of("BUILD-INFO.txt")]
  ')" \
  '["Checksums","Checksums",null]'

# Combined-total badge JSON shape (Shields endpoint / dynamic JSON).
assert_eq "badge json shape" \
  "$(jq -n --argjson total 201 '{schemaVersion:1,label:"downloads",message:($total|tostring),color:"blue"}')" \
  '{"schemaVersion":1,"label":"downloads","message":"201","color":"blue"}'

if ! grep -Fq 'release-downloads-badge.json' "${script_dir}/release-stats.sh"; then
  echo "FAIL: release-stats.sh does not write release-downloads-badge.json" >&2
  exit 1
fi
workflow="${script_dir}/../.github/workflows/release-stats.yml"
if ! grep -Fq 'release-downloads-badge.json' "${workflow}"; then
  echo "FAIL: release-stats.yml does not publish release-downloads-badge.json" >&2
  exit 1
fi
if ! grep -Fq 'seeded_histories=(' "${workflow}"; then
  echo "FAIL: release-stats.yml missing seeded_histories array" >&2
  exit 1
fi
if ! grep -Fq '"${files[@]}" "${seeded_histories[@]}"' "${workflow}"; then
  echo "FAIL: release-stats.yml publish cleanup does not clear seeded_histories" >&2
  exit 1
fi
if ! awk '
  /seeded_histories=\(/ { in_arr=1 }
  in_arr && /package-downloads-history\.jsonl/ { found=1 }
  in_arr && /\)/ { in_arr=0 }
  END { exit found ? 0 : 1 }
' "${workflow}"; then
  echo "FAIL: release-stats.yml seeded_histories omits package-downloads-history.jsonl" >&2
  exit 1
fi

echo "OK: release-stats monthly aggregation"
