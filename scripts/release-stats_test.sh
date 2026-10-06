#!/usr/bin/env bash
# Regression checks for weekly aggregation in scripts/release-stats-weekly.jq.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
jq_filter="${script_dir}/release-stats-weekly.jq"
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

# Consecutive weeks: baseline excluded; deltas sum within each ISO week.
cat > "${tmp}/history.jsonl" <<'EOF'
{"date":"2026-07-06","total":10}
{"date":"2026-07-20","total":12}
{"date":"2026-07-21","total":15}
{"date":"2026-07-27","total":15}
{"date":"2026-07-28","total":18}
EOF
assert_eq "weekly aggregation" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history.jsonl")" \
  '[{"week":"2026-W30","downloads":5},{"week":"2026-W31","downloads":3}]'

# Gap fill: W28 and W31 observed → W29/W30 appear as 0.
cat > "${tmp}/history-gap.jsonl" <<'EOF'
{"date":"2026-07-06","total":10}
{"date":"2026-07-10","total":14}
{"date":"2026-07-27","total":20}
EOF
assert_eq "gap fill" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-gap.jsonl")" \
  '[{"week":"2026-W28","downloads":4},{"week":"2026-W29","downloads":0},{"week":"2026-W30","downloads":0},{"week":"2026-W31","downloads":6}]'

# Negative cumulative correction clamps that step to zero.
cat > "${tmp}/history-clamp.jsonl" <<'EOF'
{"date":"2026-08-03","total":20}
{"date":"2026-08-04","total":17}
{"date":"2026-08-05","total":19}
EOF
assert_eq "negative delta clamp" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-clamp.jsonl")" \
  '[{"week":"2026-W32","downloads":2}]'

# Single snapshot → no weekly points yet.
printf '%s\n' '{"date":"2026-08-03","total":20}' > "${tmp}/history-one.jsonl"
assert_eq "single snapshot" \
  "$(jq -s -f "${jq_filter}" "${tmp}/history-one.jsonl")" \
  '[]'

# Chart window: last 6 weeks only (full series may be longer).
window="$(printf '%s' '[{"week":"W1","downloads":1},{"week":"W2","downloads":2},{"week":"W3","downloads":3},{"week":"W4","downloads":4},{"week":"W5","downloads":5},{"week":"W6","downloads":6},{"week":"W7","downloads":7},{"week":"W8","downloads":8}]' | jq -c '.[-6:] | { labels: [.[].week], values: [.[].downloads] }')"
assert_eq "chart last-6 window" \
  "${window}" \
  '{"labels":["W3","W4","W5","W6","W7","W8"],"values":[3,4,5,6,7,8]}'

bash -n "${script_dir}/release-stats.sh"

echo "OK: release-stats weekly aggregation"
