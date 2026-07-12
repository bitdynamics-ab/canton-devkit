#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-UP-002"

run_test() {
  echo "  Starting e2e-named-test (may take 3-5 minutes)..."
  timed 600 "$CDK" localnet up e2e-named-test \
    || { echo "FAIL step 1: up exited $?" >&2; return 1; }

  sleep 2

  docker ps --filter "label=com.docker.compose.project=canton-e2e-named-test" --format '{{.Names}}' | grep -qE "e2e-named-test" \
    || { echo "FAIL step 2: no containers with compose project label" >&2; return 1; }

  cli status e2e-named-test 2>&1 | grep -qiE "e2e-named-test" \
    || { echo "FAIL step 3: status output missing instance name" >&2; return 1; }

  cli remove e2e-named-test --force 2>/dev/null || true
  docker compose -p "canton-e2e-named-test" down --volumes 2>/dev/null || true
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "LocalNet up with name" run_test "named instance failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
