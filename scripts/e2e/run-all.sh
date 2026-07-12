#!/usr/bin/env bash
# Run all Milestone 1 E2E tests in order (local / full-suite use).
#
# Platform:  macOS (Apple Silicon) and Linux
# CLI mode:  canton-devkit localnet (standalone -- no DPM)
#
# Skipped tests:
#   M1-INST-001  Install via DPM component (DPM excluded)
#   M1-INST-002  Install standalone binary (binary already built)
#   M1-DOC-003   Doctor -- insufficient resources (requires Docker Desktop config changes)
#   M1-ISO-001   Two named instances (resource-heavy, skipped by request)
#
# Usage:
#   scripts/e2e/run-all.sh
#   CDK=./canton-devkit scripts/e2e/run-all.sh
#
# Individual tests (CI jobs):
#   scripts/e2e/m1-up-001.sh
#
# Exit 0 = all tests passed; 1 = one or more failures.

set -u

export E2E_AGGREGATE=1

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

cleanup() {
  echo ""
  section "CLEANUP"
  e2e_force_cleanup_instances
  print_summary
}
trap cleanup EXIT

e2e_require_binary
e2e_require_docker
e2e_preclean_instances

echo "Canton DevKit E2E -- Milestone 1"
echo "Platform: $(uname -s) ($(uname -m))"
echo "Binary:   $CDK"
echo "Docker:   $(docker --version 2>/dev/null)"
echo ""

section "PHASE 1: Binary and preflight checks"
# shellcheck source=m1-inst-003.sh
source "${E2E_DIR}/m1-inst-003.sh"
# shellcheck source=m1-doc-001.sh
source "${E2E_DIR}/m1-doc-001.sh"
# shellcheck source=m1-doc-002.sh
source "${E2E_DIR}/m1-doc-002.sh"

section "PHASE 2: Default instance lifecycle (e2e-test-default)"
# shellcheck source=m1-up-001.sh
source "${E2E_DIR}/m1-up-001.sh"
# shellcheck source=m1-sts-001.sh
source "${E2E_DIR}/m1-sts-001.sh"
# shellcheck source=m1-log-001.sh
source "${E2E_DIR}/m1-log-001.sh"
# shellcheck source=m1-env-001.sh
source "${E2E_DIR}/m1-env-001.sh"
# shellcheck source=m1-rst-001.sh
source "${E2E_DIR}/m1-rst-001.sh"
# shellcheck source=m1-stp-001.sh
source "${E2E_DIR}/m1-stp-001.sh"
# shellcheck source=m1-snp-001.sh
source "${E2E_DIR}/m1-snp-001.sh"
# shellcheck source=m1-dwn-001.sh
source "${E2E_DIR}/m1-dwn-001.sh"
# shellcheck source=m1-cln-001.sh
source "${E2E_DIR}/m1-cln-001.sh"

section "PHASE 3: Named instance and version tests"
# shellcheck source=m1-up-002.sh
source "${E2E_DIR}/m1-up-002.sh"
# shellcheck source=m1-up-003.sh
source "${E2E_DIR}/m1-up-003.sh"
# shellcheck source=m1-lst-001.sh
source "${E2E_DIR}/m1-lst-001.sh"

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
exit 0
