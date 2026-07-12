#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-UP-003"

run_test() {
  echo "  Starting e2e-version-test with --version $SPLICE_VERSION (may take 3-5 minutes)..."
  timed 600 "$CDK" localnet up e2e-version-test --version "$SPLICE_VERSION" \
    || { echo "FAIL step 1: up --version exited $?" >&2; return 1; }

  sleep 2

  cli status e2e-version-test 2>&1 | grep -qE "$SPLICE_VERSION" \
    || { echo "FAIL step 2: status missing version $SPLICE_VERSION" >&2; return 1; }

  if "$CDK" localnet up e2e-bad-version --version "0.0.0-nonexistent" >/dev/null 2>&1; then
    cli remove e2e-bad-version --force 2>/dev/null || true
    echo "FAIL step 3: invalid version should have been rejected" >&2
    return 1
  fi
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "LocalNet up with --version" run_test "version test failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
