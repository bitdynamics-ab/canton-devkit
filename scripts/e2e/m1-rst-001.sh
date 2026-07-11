#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-RST-001"

run_test() {
  ensure_instance e2e-test-default || return 1

  echo "  Restarting e2e-test-default (may take a few minutes)..."
  timed 600 "$CDK" localnet restart e2e-test-default \
    || { echo "FAIL step 1: full restart exited $?" >&2; return 1; }

  cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)" \
    || { echo "FAIL step 1: not healthy after full restart" >&2; return 1; }

  timed 600 "$CDK" localnet restart e2e-test-default --service canton \
    || { echo "FAIL step 2: single-service restart exited $?" >&2; return 1; }

  wait_for_healthy e2e-test-default \
    || { echo "FAIL step 2: not healthy after single-service restart" >&2; return 1; }
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Restart full + single service" run_test "restart or post-restart health check failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
