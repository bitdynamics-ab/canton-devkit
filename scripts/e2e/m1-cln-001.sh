#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-CLN-001"

run_test() {
  echo "  Clean test (up + clean --force cycle)..."
  cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)" || \
    timed 600 "$CDK" localnet up e2e-test-default \
    || { echo "FAIL step 1: could not bring instance up for clean test" >&2; return 1; }

  cli clean --name e2e-test-default --force \
    || { echo "FAIL step 2: clean --force exited $?" >&2; return 1; }

  if docker ps -a --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 3a: containers remain after clean" >&2
    return 1
  fi
  if docker volume ls --format '{{.Name}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 3b: volumes remain after clean" >&2
    return 1
  fi
  if docker network ls --format '{{.Name}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 3c: networks remain after clean" >&2
    return 1
  fi
}

if e2e_is_aggregate_mode; then
  if ! e2e_run_with_result "$TEST_ID" "Clean removes all resources" run_test "clean failed or resources remain"; then
    docker compose -p "canton-e2e-test-default" down --volumes 2>/dev/null || true
  fi
else
  if ( set -e; run_test ); then
    echo "PASS ${TEST_ID}"
    exit 0
  fi
  docker compose -p "canton-e2e-test-default" down --volumes 2>/dev/null || true
  echo "FAIL ${TEST_ID}" >&2
  exit 1
fi
