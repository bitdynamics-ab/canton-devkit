#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-STP-001"

run_test() {
  ensure_instance e2e-test-default || return 1

  echo "  Stop/start cycle (may take a few minutes)..."
  cli stop e2e-test-default \
    || { echo "FAIL step 1: stop exited $?" >&2; return 1; }

  sleep 5

  docker ps -a --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default" \
    || { echo "FAIL step 2: containers removed by stop (should be preserved)" >&2; return 1; }
  if docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 2: containers still running after stop" >&2
    return 1
  fi

  timed 600 "$CDK" localnet start e2e-test-default \
    || { echo "FAIL step 3: start exited $?" >&2; return 1; }

  wait_for_healthy e2e-test-default \
    || { echo "FAIL step 3: not healthy after start" >&2; return 1; }
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Stop keeps containers; start restores them" run_test "stop/start cycle failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
