#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-SNP-001"

run_test() {
  ensure_instance e2e-test-default || return 1

  echo "  Snapshot/restore cycle (may take a few minutes)..."
  cli snapshot e2e-test-default --to "$SNAPSHOT_PATH" \
    || { echo "FAIL step 1: snapshot command failed" >&2; return 1; }
  [ -f "$SNAPSHOT_PATH" ] && [ -s "$SNAPSHOT_PATH" ] \
    || { echo "FAIL step 1: snapshot file missing or empty" >&2; return 1; }

  # Tear down with `down` (NOT `remove`): restore loads a dump into the
  # instance's EXISTING Compose-owned Postgres volume, so the volume must
  # survive. `remove` reclaims volumes and would leave restore with nothing
  # to load into (restore refuses to create a volume out of band).
  cli down e2e-test-default \
    || { echo "FAIL step 2: down before restore failed" >&2; return 1; }

  cli restore e2e-test-default --from "$SNAPSHOT_PATH" \
    || { echo "FAIL step 3: restore command failed" >&2; return 1; }

  timed 600 "$CDK" localnet up e2e-test-default || {
    cli status e2e-test-default 2>&1 | grep -qiE "(partial|syncing|healthy|running)" \
      || { echo "FAIL step 4: post-restore up failed and services not even partially up" >&2; return 1; }
  }
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Snapshot and restore" run_test "snapshot/restore cycle failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
