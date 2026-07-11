#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-ENV-001"

run_test() {
  ensure_instance e2e-test-default || return 1

  local OUTPUT
  OUTPUT=$(cli env e2e-test-default 2>&1) \
    || { echo "FAIL step 1: env exited $?" >&2; return 1; }

  echo "$OUTPUT" | grep -qE "^(export )?[A-Z_]+=" \
    || { echo "FAIL step 2: no KEY=value lines in env output" >&2; return 1; }

  echo "$OUTPUT" | grep -qiE "(LEDGER|JSON_API|ADMIN|PARTICIPANT|CANTON)" \
    || { echo "FAIL step 3: env output missing expected variable names" >&2; return 1; }
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Env export outputs valid config" run_test "env output format/content unexpected"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
