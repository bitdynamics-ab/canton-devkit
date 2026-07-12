#!/usr/bin/env bash
# DPM-DAR-001: `dpm localnet dar build-upload` inside a Daml project.
#
# Regression test for issue #230: under `dpm localnet …`, DPM injects
# DPM_RESOLUTION_FILE pointing at a temp resolution file it already
# wrote. build-upload shells out to a nested `dpm build`, which used to
# inherit that var and abort with:
#
#   open /var/folders/.../T/<n>.yaml: file exists
#   dar build-upload: build failed: exit status 1
#
# --build-only exercises the exact build step that regressed without
# needing a running LocalNet (the failure happened before any upload).
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=dpm-lib.sh
source "${E2E_DIR}/dpm-lib.sh"

TEST_ID="DPM-DAR-001"

run_test() {
  # Missing dpm → SKIP (return 2), not a hard failure.
  e2e_require_dpm || return 2

  if [ -z "${COMPONENT_DIR}" ] || [ ! -d "${COMPONENT_DIR}" ]; then
    echo "COMPONENT_DIR unset or not a directory (${COMPONENT_DIR}) — cannot resolve the DevKit component to install" >&2
    return 2
  fi

  local proj
  proj="$(e2e_make_dpm_project "${COMPONENT_DIR}")"
  # shellcheck disable=SC2064
  trap "rm -rf '${proj}'" RETURN

  ( cd "$proj" && "$DPM" install package ) \
    || { echo "dpm install package failed" >&2; return 1; }

  # The failing command from issue #230 (build-only skips the upload RPC
  # so no LocalNet is required). Capture combined output for assertions.
  local out rc
  out="$( cd "$proj" && dpm_localnet dar build-upload --build-only 2>&1 )"
  rc=$?

  echo "$out"

  if [ "$rc" -ne 0 ]; then
    echo "build-upload exited ${rc} (expected 0)" >&2
    return 1
  fi

  # The specific regression signature must be absent.
  if echo "$out" | grep -q "file exists"; then
    echo "build-upload printed 'file exists' — DPM_RESOLUTION_FILE leaked into the nested build (issue #230)" >&2
    return 1
  fi

  # A DAR must have been produced.
  if ! ls "${proj}/.daml/dist/"*.dar >/dev/null 2>&1; then
    echo "no DAR produced in ${proj}/.daml/dist/" >&2
    return 1
  fi

  return 0
}

if e2e_is_aggregate_mode; then
  # In aggregate mode a SKIP (rc 2) should not count as a failure.
  if ( set -e; run_test ); then
    pass "${TEST_ID}: build-upload inside a Daml project (issue #230)"
  else
    rc=$?
    if [ "$rc" -eq 2 ]; then
      skip "${TEST_ID}: build-upload inside a Daml project (issue #230)" "dpm unavailable or COMPONENT_DIR unset"
    else
      fail "${TEST_ID}: build-upload inside a Daml project (issue #230)" "build-upload failed"
    fi
  fi
else
  e2e_run_standalone "$TEST_ID" run_test
fi
