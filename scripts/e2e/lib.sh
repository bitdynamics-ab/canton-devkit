#!/usr/bin/env bash
# Shared helpers for Milestone 1 E2E tests.
# shellcheck shell=bash

CDK="${CDK:-bin/canton-devkit}"
SPLICE_VERSION="${SPLICE_VERSION:-0.6.12}"
SNAPSHOT_PATH="${SNAPSHOT_PATH:-/tmp/e2e-m1-snapshot.tgz}"

E2E_INSTANCE_NAMES=(e2e-test-default e2e-named-test e2e-version-test e2e-bad-version)

# Helper: invoke the localnet subcommand via the CDK binary.
cli() { "$CDK" localnet "$@"; }

# Wrapper around `timeout` that uses --foreground on Linux so the
# timeout signal only hits the child process, not the parent shell.
if [[ "$(uname -s)" == "Linux" ]]; then
  timed() { local s="$1"; shift; timeout --foreground "$s" "$@"; }
else
  timed() { local s="$1"; shift; timeout "$s" "$@"; }
fi

# Aggregate-mode counters (run-all.sh sets E2E_AGGREGATE=1).
: "${PASS:=0}"
: "${FAIL:=0}"
: "${SKIP:=0}"
if [ -z "${E2E_LIB_LOADED:-}" ]; then
  declare -a RESULTS=()
  E2E_LIB_LOADED=1
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

e2e_require_binary() {
  if [ ! -x "$CDK" ]; then
    echo "ERROR: binary not found at $CDK -- run 'make build' first" >&2
    exit 1
  fi
}

e2e_require_docker() {
  docker info >/dev/null 2>&1 || {
    echo "ERROR: Docker daemon not reachable" >&2
    exit 1
  }
}

# Bring instance to a usable state; no-op when already healthy.
ensure_instance() {
  local name="$1"
  local status_out

  status_out=$(cli status "$name" 2>&1 || true)
  if echo "$status_out" | grep -qiE "(healthy|ready|running|partial|syncing)"; then
    return 0
  fi

  if docker ps -a --filter "label=com.docker.compose.project=canton-${name}" --format '{{.Names}}' \
    | grep -qE "${name}"; then
    timed 600 "$CDK" localnet start "$name" \
      || { echo "ensure_instance: start ${name} failed" >&2; return 1; }
  else
    timed 600 "$CDK" localnet up "$name" \
      || { echo "ensure_instance: up ${name} failed" >&2; return 1; }
  fi

  sleep 2
}

wait_for_healthy() {
  local name="$1"
  local i ok=false

  for i in 1 2 3 4 5; do
    if cli status "$name" 2>&1 | grep -qiE "(healthy|ready|running)"; then
      ok=true
      break
    fi
    sleep 5
  done
  $ok
}

e2e_preclean_instances() {
  local name
  for name in "${E2E_INSTANCE_NAMES[@]}"; do
    cli down "$name" 2>/dev/null || true
    cli clean --name "$name" --force 2>/dev/null || true
  done
}

e2e_force_cleanup_instances() {
  local name
  for name in "${E2E_INSTANCE_NAMES[@]}"; do
    cli clean --name "$name" --force 2>/dev/null || true
    docker compose -p "canton-${name}" down --volumes 2>/dev/null || true
  done
  rm -f "$SNAPSHOT_PATH"
}

print_summary() {
  echo ""
  echo "======================================================="
  echo " E2E Milestone 1 Results"
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
    echo "## E2E Milestone 1 Results"
    echo ""
    echo "**Passed:** ${PASS} &nbsp;&nbsp; **Failed:** ${FAIL} &nbsp;&nbsp; **Skipped:** ${SKIP}"
    echo ""
    echo "| Result | Test |"
    echo "| :----: | :--- |"
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

# Run a test function in standalone mode (exit 0/1).
e2e_run_standalone() {
  local test_id="$1"
  local fn="$2"

  echo "${test_id}"
  if ( set -e; "$fn" ); then
    echo "PASS ${test_id}"
    exit 0
  fi
  echo "FAIL ${test_id}" >&2
  exit 1
}
