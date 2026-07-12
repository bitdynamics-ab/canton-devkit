#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-LOG-001"

run_test() {
  ensure_instance e2e-test-default || return 1

  local LOG_OUTPUT LINE_COUNT SVC_OUTPUT SVC_LINES
  LOG_OUTPUT=$("$CDK" localnet logs e2e-test-default --tail 50 2>&1) || true
  LINE_COUNT=$(echo "$LOG_OUTPUT" | wc -l | tr -d ' ')
  [ "$LINE_COUNT" -gt 1 ] \
    || { echo "FAIL step 1: full logs empty (got $LINE_COUNT lines)" >&2; return 1; }

  SVC_OUTPUT=$("$CDK" localnet logs e2e-test-default --service canton --tail 20 2>&1) || true
  SVC_LINES=$(echo "$SVC_OUTPUT" | wc -l | tr -d ' ')
  [ "$SVC_LINES" -gt 1 ] \
    || { echo "FAIL step 2: service-filtered logs empty (got $SVC_LINES lines)" >&2; return 1; }

  "$CDK" localnet logs e2e-test-default --service nonexistent-service-xyz --tail 5 >/dev/null 2>&1 || true
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Logs -- full and service-filtered" run_test "logs output unexpected"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
