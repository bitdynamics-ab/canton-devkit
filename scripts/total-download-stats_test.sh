#!/usr/bin/env bash
# Regression checks for scripts/total-download-stats.sh and merge jq.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
jq_filter="${script_dir}/total-download-stats-monthly.jq"
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

# --- Merge jq -------------------------------------------------------------
assert_eq "sum overlapping months" \
  "$(jq -n -f "${jq_filter}" \
    --argjson standalone '[{"month":"2026-09","downloads":10},{"month":"2026-10","downloads":20}]' \
    --argjson dpm '[{"month":"2026-09","downloads":5},{"month":"2026-10","downloads":7}]')" \
  '[{"month":"2026-09","standalone":10,"dpm":5,"downloads":15},{"month":"2026-10","standalone":20,"dpm":7,"downloads":27}]'

assert_eq "gap fill across channels" \
  "$(jq -n -f "${jq_filter}" \
    --argjson standalone '[{"month":"2026-07","downloads":4}]' \
    --argjson dpm '[{"month":"2026-09","downloads":6}]')" \
  '[{"month":"2026-07","standalone":4,"dpm":0,"downloads":4},{"month":"2026-08","standalone":0,"dpm":0,"downloads":0},{"month":"2026-09","standalone":0,"dpm":6,"downloads":6}]'

assert_eq "empty both" \
  "$(jq -n -f "${jq_filter}" --argjson standalone '[]' --argjson dpm '[]')" \
  '[]'

assert_eq "standalone only" \
  "$(jq -n -f "${jq_filter}" \
    --argjson standalone '[{"month":"2026-08","downloads":3}]' \
    --argjson dpm '[]')" \
  '[{"month":"2026-08","standalone":3,"dpm":0,"downloads":3}]'

# --- End-to-end against fixture histories ---------------------------------
out="${tmp}/out"
mkdir -p "${out}"

# Two daily snapshots per channel so release-stats-monthly.jq emits deltas.
cat > "${out}/release-downloads-history.jsonl" <<'EOF'
{"date":"2026-09-01","total":100}
{"date":"2026-09-30","total":110}
{"date":"2026-10-15","total":130}
{"date":"2026-11-01","total":140}
EOF

cat > "${out}/package-downloads-history.jsonl" <<'EOF'
{"date":"2026-09-01","total":500}
{"date":"2026-09-30","total":520}
{"date":"2026-10-15","total":560}
{"date":"2026-11-01","total":580}
EOF

OUT_DIR="${out}" bash "${script_dir}/total-download-stats.sh" >/dev/null

svg="${out}/total-downloads-monthly.svg"
md="${out}/total-downloads.md"
[ -f "${svg}" ] || { echo "FAIL: missing svg" >&2; exit 1; }
[ -f "${md}" ] || { echo "FAIL: missing md" >&2; exit 1; }

assert_file_contains "svg title" "${svg}" "Total monthly downloads"
assert_file_contains "md header" "${md}" "| Month | Standalone | DPM | Total |"
# Sept: standalone 10 + dpm 20 = 30; Oct: 20+40=60; Nov: 10+20=30
assert_file_contains "md sept total" "${md}" "| 2026-09 | 10 | 20 | 30 |"
assert_file_contains "md oct total" "${md}" "| 2026-10 | 20 | 40 | 60 |"
assert_file_contains "md nov total" "${md}" "| 2026-11 | 10 | 20 | 30 |"

# Missing histories still produce placeholder outputs.
empty_out="${tmp}/empty"
mkdir -p "${empty_out}"
OUT_DIR="${empty_out}" bash "${script_dir}/total-download-stats.sh" >/dev/null
[ -f "${empty_out}/total-downloads-monthly.svg" ] || { echo "FAIL: missing empty svg" >&2; exit 1; }
assert_file_contains "empty md" "${empty_out}/total-downloads.md" "need history snapshots"

bash -n "${script_dir}/total-download-stats.sh"

# Workflows must publish the combined chart.
for wf in release-stats.yml package-stats.yml; do
  path="${script_dir}/../.github/workflows/${wf}"
  if ! grep -Fq 'total-downloads-monthly.svg' "${path}"; then
    echo "FAIL: ${wf} does not publish total-downloads-monthly.svg" >&2
    exit 1
  fi
  if ! grep -Fq 'total-download-stats.sh' "${path}"; then
    echo "FAIL: ${wf} does not run total-download-stats.sh" >&2
    exit 1
  fi
done

echo "OK: total-download-stats"
