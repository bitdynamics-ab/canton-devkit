#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-STS-001"

run_test() {
  ensure_instance e2e-test-default || return 1

  local OUTPUT
  OUTPUT=$(cli status e2e-test-default 2>&1) \
    || { echo "FAIL step 1: status exited $?" >&2; return 1; }
  echo "$OUTPUT" | grep -qiE "(healthy|running|ready)" \
    || { echo "FAIL step 1: status missing healthy/running/ready" >&2; return 1; }

  if cli status nonexistent-localnet-xyz >/dev/null 2>&1; then
    echo "FAIL step 2: status for non-existent instance should fail" >&2
    return 1
  fi
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Status shows healthy services" run_test "status output missing expected fields or non-existent check failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
