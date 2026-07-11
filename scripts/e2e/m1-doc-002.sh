#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary

TEST_ID="M1-DOC-002"

run_test() {
  local MINIMAL_PATH="" dir OUTPUT
  IFS=':' read -ra DIRS <<< "$PATH"
  for dir in "${DIRS[@]}"; do
    if [ -d "$dir" ] && ! [ -x "$dir/docker" ]; then
      MINIMAL_PATH="${MINIMAL_PATH:+$MINIMAL_PATH:}$dir"
    fi
  done

  if PATH="$MINIMAL_PATH" "$CDK" localnet doctor >/dev/null 2>&1; then
    return 1
  fi

  OUTPUT=$(PATH="$MINIMAL_PATH" "$CDK" localnet doctor 2>&1 || true)
  echo "$OUTPUT" | grep -qiE "(install docker|docker not found|docker desktop|docker cli)"
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Doctor -- Docker not installed" run_test "doctor did not fail or missing remediation message"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
