#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-UP-001"

run_test() {
  echo "  Starting e2e-test-default (may take 3-5 minutes)..."
  timed 600 "$CDK" localnet up e2e-test-default \
    || { echo "FAIL step 1: up exited $?" >&2; return 1; }

  sleep 2

  cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)" \
    || { echo "FAIL step 2: status missing healthy/ready/running" >&2; return 1; }

  docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default" \
    || { echo "FAIL step 3: no containers with compose project label" >&2; return 1; }

  docker compose ls 2>/dev/null | grep -qE "e2e-test-default" \
    || { echo "FAIL step 4: compose project not listed" >&2; return 1; }
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "LocalNet up (default)" run_test "up/status/docker check failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
