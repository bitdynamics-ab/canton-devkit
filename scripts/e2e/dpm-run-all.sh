#!/usr/bin/env bash
# Run all `dpm localnet` E2E tests in order (local / full-suite use).
#
# Usage:
#   COMPONENT_DIR=/abs/path/to/devkit-component scripts/e2e/dpm-run-all.sh
#   DPM=/path/to/dpm COMPONENT_DIR=./.tmp/comp scripts/e2e/dpm-run-all.sh
#
# COMPONENT_DIR is a local DevKit component directory (bin/canton-devkit +
# a rendered component.yaml). See scripts/e2e/dpm-build-component.sh for a
# helper that assembles one from `make build`.
set -u

export E2E_AGGREGATE=1

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=dpm-lib.sh
source "${E2E_DIR}/dpm-lib.sh"

cleanup() {
  echo ""
  section "CLEANUP"
  print_summary
}
trap cleanup EXIT

echo "Canton DevKit E2E -- dpm localnet"
echo "Platform:  $(uname -s) ($(uname -m))"
echo "DPM:       ${DPM}"
echo "Component: ${COMPONENT_DIR:-<unset>}"
echo ""

section "DAR"
# shellcheck source=dpm-dar-001.sh
source "${E2E_DIR}/dpm-dar-001.sh"

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
exit 0
