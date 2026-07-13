#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-DOC-001"

run_test() {
  local OUTPUT
  OUTPUT=$(cli doctor 2>&1)
  echo "$OUTPUT" | grep -qiE "(docker cli|docker daemon|compose|port|disk|memory)"
  ! echo "$OUTPUT" | grep -qE "^  ✗"
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Doctor -- all checks pass" run_test "doctor reported failures"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
