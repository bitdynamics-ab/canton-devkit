#!/usr/bin/env bash
# Shared helpers for the `dpm localnet` E2E tests (DPM component mode).
#
# Mirrors the structure of the Milestone 1 lib (scripts/e2e/lib.sh): each
# test lives in its own dpm-*.sh script, sources this file, and runs in
# either aggregate mode (dpm-run-all.sh sets E2E_AGGREGATE=1) or
# standalone mode (one script = one CI job).
# shellcheck shell=bash

# DPM CLI under test. The component build-upload path shells out to a
# nested `dpm build`; that nested invocation is exactly what issue #230
# regressed on.
DPM="${DPM:-dpm}"

# COMPONENT_DIR is a local DevKit component directory (bin/ + rendered
# component.yaml) the test project installs via a file-based component
# reference ({name, path}). Resolving from a local path keeps the test
# fully hermetic — no OCI registry, no TLS — and validates the binary
# built in THIS run rather than a released one. The CI setup step
# assembles it from `make build` + packaging/component.yaml.tmpl.
COMPONENT_DIR="${COMPONENT_DIR:-}"

# Name the file-based component is registered under in daml.yaml. Must
# match the command the component publishes (`localnet`); DPM keys the
# component by name, the value is irrelevant to `dpm localnet` dispatch.
COMPONENT_NAME="${COMPONENT_NAME:-canton-devkit}"

# Extra components the test project needs to actually compile Daml.
DAMLC_COMPONENT="${DAMLC_COMPONENT:-damlc:3.5.2}"
DAML_SCRIPT_COMPONENT="${DAML_SCRIPT_COMPONENT:-daml-script:3.5.2}"

# Invoke `dpm localnet …`.
dpm_localnet() { "$DPM" localnet "$@"; }

# Aggregate-mode counters (dpm-run-all.sh sets E2E_AGGREGATE=1).
: "${PASS:=0}"
: "${FAIL:=0}"
: "${SKIP:=0}"
if [ -z "${E2E_DPM_LIB_LOADED:-}" ]; then
  declare -a RESULTS=()
  E2E_DPM_LIB_LOADED=1
fi

e2e_is_aggregate_mode() {
  [ "${E2E_AGGREGATE:-0}" = "1" ]
}

pass() {
  PASS=$((PASS + 1))
  RESULTS+=("PASS  $1")
  printf '  PASS  %s\n' "$1"
}

fail() {
  FAIL=$((FAIL + 1))
  RESULTS+=("FAIL  $1  -- $2")
  printf '  FAIL  %s -- %s\n' "$1" "$2" >&2
}

skip() {
  SKIP=$((SKIP + 1))
  RESULTS+=("SKIP  $1  -- $2")
  printf '  SKIP  %s -- %s\n' "$1" "$2"
}

section() {
  echo ""
  printf '== %s\n' "$1"
}

e2e_require_dpm() {
  command -v "$DPM" >/dev/null 2>&1 || {
    echo "dpm not found on PATH (DPM=$DPM)" >&2
    return 1
  }
}

# Scaffold a minimal, compilable Daml project that references the local
# DevKit component under test. Echoes the created project directory on
# stdout. Scratch lives under the repo (.tmp/) per AGENTS.md, not /tmp.
#   $1 = absolute path to the DevKit component directory
e2e_make_dpm_project() {
  local component_dir="$1"
  local scratch dir
  scratch="${E2E_SCRATCH_DIR:-.tmp/e2e-dpm}"
  mkdir -p "$scratch"
  dir="$(mktemp -d "${scratch}/dar-XXXXXX")"

  mkdir -p "${dir}/daml"
  cat > "${dir}/daml/Main.daml" <<'DAML'
module Main where

import Daml.Script

setup : Script ()
setup = pure ()
DAML

  cat > "${dir}/daml.yaml" <<EOF
name: e2e-dpm-dar
source: daml
init-script: Main:setup
version: 0.0.1
dependencies:
  - daml-prim
  - daml-stdlib
  - daml-script
components:
  - ${DAMLC_COMPONENT}
  - ${DAML_SCRIPT_COMPONENT}
  - name: ${COMPONENT_NAME}
    path: ${component_dir}
EOF

  echo "$dir"
}

print_summary() {
  echo ""
  echo "======================================================="
  echo " E2E dpm localnet Results"
  printf ' PASSED: %d  FAILED: %d  SKIPPED: %d\n' "$PASS" "$FAIL" "$SKIP"
  echo "======================================================="
  local r
  for r in "${RESULTS[@]}"; do
    echo "  $r"
  done
  echo "======================================================="
  write_step_summary
}

write_step_summary() {
  [ -n "${GITHUB_STEP_SUMMARY:-}" ] || return 0

  {
    echo "## E2E dpm localnet Results"
    echo ""
    echo "**Passed:** ${PASS} &nbsp;&nbsp; **Failed:** ${FAIL} &nbsp;&nbsp; **Skipped:** ${SKIP}"
    echo ""
    echo "| Result | Test |"
    echo "| :----: | :--- |"
    local r icon rest test
    for r in "${RESULTS[@]}"; do
      case "$r" in
        PASS*)  icon="OK";   rest="${r#PASS  }" ;;
        FAIL*)  icon="FAIL"; rest="${r#FAIL  }" ;;
        SKIP*)  icon="SKIP"; rest="${r#SKIP  }" ;;
        *)      icon="";     rest="$r" ;;
      esac
      test="${rest%%  -- *}"
      printf '| %s | %s |\n' "$icon" "$test"
    done
  } >> "$GITHUB_STEP_SUMMARY"
}

# Run a test function in aggregate mode (pass/fail counters).
#   $1 test id  $2 label  $3 fn  $4 optional failure reason
e2e_run_with_result() {
  local test_id="$1"
  local label="$2"
  local fn="$3"

  if ( set -e; "$fn" ); then
    pass "${test_id}: ${label}"
    return 0
  fi
  fail "${test_id}: ${label}" "${4:-test failed}"
  return 1
}

# Run a test function in standalone mode (exit 0/1). A return code of 2
# from the function is treated as SKIP so a missing dpm doesn't fail CI.
e2e_run_standalone() {
  local test_id="$1"
  local fn="$2"
  local rc

  echo "${test_id}"
  ( set -e; "$fn" )
  rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "PASS ${test_id}"
    exit 0
  fi
  if [ "$rc" -eq 2 ]; then
    echo "SKIP ${test_id}"
    exit 0
  fi
  echo "FAIL ${test_id}" >&2
  exit 1
}
