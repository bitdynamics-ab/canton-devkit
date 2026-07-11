#!/usr/bin/env bash
set -u

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${E2E_DIR}/lib.sh"

e2e_require_binary
e2e_require_docker

TEST_ID="M1-INST-003"

run_test() {
  "$CDK" --version 2>&1 | grep -qE "([0-9]+\.[0-9]+\.[0-9]+|dev)"

  local HELP
  HELP=$(cli --help 2>&1)
  local cmd
  for cmd in up start stop down restart pause resume remove status logs snapshot restore doctor env list creds; do
    echo "$HELP" | grep -qiE "$cmd"
  done
  cli unpause --help >/dev/null 2>&1
  cli clean --help >/dev/null 2>&1

  case "$(uname -s)" in
    Darwin) file "$CDK" 2>/dev/null | grep -qiE "Mach-O" ;;
    Linux)  file "$CDK" 2>/dev/null | grep -qiE "ELF" ;;
    *)      file "$CDK" 2>/dev/null | grep -qiE "(Mach-O|ELF|PE32)" ;;
  esac
}

if e2e_is_aggregate_mode; then
  e2e_run_with_result "$TEST_ID" "Verify binary" run_test "version/help/file check failed"
else
  e2e_run_standalone "$TEST_ID" run_test
fi
