#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-DWN-001"

run_test() {
  if ! docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    timed 600 "$CDK" localnet up e2e-test-default \
      || { echo "FAIL: could not bring instance up for down test" >&2; return 1; }
    sleep 2
  fi

  local NON_DEVKIT_BEFORE NON_DEVKIT_AFTER
  NON_DEVKIT_BEFORE=$(docker ps --format '{{.Names}}' | grep -cv "e2e-test")

  cli down e2e-test-default \
    || { echo "FAIL step 1: down exited $?" >&2; return 1; }

  sleep 5

  if docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 2: containers still running after down" >&2
    docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' >&2
    return 1
  fi

  NON_DEVKIT_AFTER=$(docker ps --format '{{.Names}}' | grep -cv "e2e-test")
  [ "$NON_DEVKIT_BEFORE" -eq "$NON_DEVKIT_AFTER" ] \
    || { echo "FAIL step 3: non-devkit container count changed ($NON_DEVKIT_BEFORE -> $NON_DEVKIT_AFTER)" >&2; return 1; }
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Down stops instance cleanly" run_test "down failed or containers remain"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
