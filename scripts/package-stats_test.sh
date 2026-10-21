#!/usr/bin/env bash
# Regression checks for scripts/package-stats.sh and package-stats-daily.jq.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
jq_filter="${script_dir}/package-stats-daily.jq"
fixture="${script_dir}/testdata/package-stats-page.html"
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

assert_file_contains() {
  local name="$1" file="$2" needle="$3"
  if ! grep -Fq "${needle}" "${file}"; then
    echo "FAIL: ${name} — ${file} missing: ${needle}" >&2
    exit 1
  fi
}

# --- Daily jq -------------------------------------------------------------
cat > "${tmp}/history.jsonl" <<'EOF'
{"date":"2026-07-06","total":10}
{"date":"2026-07-07","total":12}
{"date":"2026-07-08","total":15}
{"date":"2026-07-09","total":18}
EOF
assert_eq "daily aggregation" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history.jsonl")" \
  '[{"date":"2026-07-07","downloads":2},{"date":"2026-07-08","downloads":3},{"date":"2026-07-09","downloads":3}]'

cat > "${tmp}/history-clamp.jsonl" <<'EOF'
{"date":"2026-08-03","total":20}
{"date":"2026-08-04","total":17}
{"date":"2026-08-05","total":19}
EOF
assert_eq "negative delta clamp" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-clamp.jsonl")" \
  '[{"date":"2026-08-04","downloads":0},{"date":"2026-08-05","downloads":2}]'

printf '%s\n' '{"date":"2026-08-03","total":20}' > "${tmp}/history-one.jsonl"
assert_eq "single snapshot" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-one.jsonl")" \
  '[]'

# Chart window: last 30 days only (full series may be longer).
window="$(printf '%s' "$(jq -n '[range(1;41) | {date: ("2026-01-" + (if . < 10 then "0\(.)" else "\(.)" end)), downloads: .}]')" | jq -c '.[-30:] | { labels: [.[].date[5:]], values: [.[].downloads] }')"
assert_eq "chart last-30 window length" \
  "$(printf '%s' "${window}" | jq '.labels | length')" \
  '30'

# --- End-to-end against HTML fixture --------------------------------------
out="${tmp}/out"
mkdir -p "${out}"
OUT_DIR="${out}" \
  PACKAGE_HTML_FILE="${fixture}" \
  SNAPSHOT_DATE=2026-10-20 \
  bash "${script_dir}/package-stats.sh" >/dev/null

history="${out}/package-downloads-history.jsonl"
md="${out}/package-downloads.md"
svg="${out}/package-downloads-daily.svg"

[ -f "${history}" ] || { echo "FAIL: missing history" >&2; exit 1; }
[ -f "${md}" ] || { echo "FAIL: missing markdown" >&2; exit 1; }
[ -f "${svg}" ] || { echo "FAIL: missing svg" >&2; exit 1; }

# Fixture total=100; sparkline sums to 100 → first sparkline day starts at 0+10.
assert_eq "history length after seed" \
  "$(jq -s 'length' "${history}")" \
  '6'

assert_eq "today row" \
  "$(jq -c 'select(.date=="2026-10-20")' "${history}")" \
  '{"date":"2026-10-20","total":100,"last30Days":100}'

assert_eq "first seeded row" \
  "$(jq -c 'select(.date=="2026-08-01")' "${history}")" \
  '{"date":"2026-08-01","total":10,"last30Days":null}'

assert_file_contains "md total" "${md}" "**Total downloads:** 100"
assert_file_contains "md day" "${md}" "| 2026-10-20 | 20 |"
assert_file_contains "svg title" "${svg}" "Daily DPM package downloads"

# Idempotent same-day re-run must not duplicate rows.
OUT_DIR="${out}" \
  PACKAGE_HTML_FILE="${fixture}" \
  SNAPSHOT_DATE=2026-10-20 \
  bash "${script_dir}/package-stats.sh" >/dev/null
assert_eq "idempotent history length" \
  "$(jq -s 'length' "${history}")" \
  '6'

# With existing history (>=2 rows), a new day only upserts today — no re-seed.
OUT_DIR="${out}" \
  PACKAGE_HTML_FILE="${fixture}" \
  SNAPSHOT_DATE=2026-10-21 \
  bash "${script_dir}/package-stats.sh" >/dev/null
# Fixture still reports total=100 on 10-21; length grows by one.
assert_eq "next-day history length" \
  "$(jq -s 'length' "${history}")" \
  '7'
assert_eq "next-day row" \
  "$(jq -c 'select(.date=="2026-10-21")' "${history}")" \
  '{"date":"2026-10-21","total":100,"last30Days":100}'

bash -n "${script_dir}/package-stats.sh"

echo "OK: package-stats"
