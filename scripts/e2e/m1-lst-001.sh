#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-LST-001"

run_test() {
  ensure_instance e2e-version-test || return 1

  local OUTPUT
  OUTPUT=$(cli list 2>&1)

  echo "$OUTPUT" | grep -qE "e2e-(named-test|version-test)" \
    || { echo "FAIL step 1: no test instances in list output" >&2; return 1; }

  echo "$OUTPUT" | grep -qiE "(NAME|SPLICE|STATUS)" \
    || { echo "FAIL step 2: list output missing expected columns" >&2; return 1; }
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "List discovers running instances" run_test "list output missing expected instances"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
