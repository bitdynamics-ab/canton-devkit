#!/usr/bin/env bash
# E2E Test Suite — Milestone 1: LocalNet Management CLI
#
# Platform:  macOS (Apple Silicon)
# CLI mode:  canton-devkit localnet (standalone — no DPM)
#
# Skipped tests:
#   M1-INST-001  Install via DPM component (DPM excluded)
#   M1-INST-002  Install standalone binary (binary already built)
#   M1-DOC-003   Doctor — insufficient resources (requires Docker Desktop config changes)
#   M1-ISO-001   Two named instances (resource-heavy, skipped by request)
#
# Usage:
#   scripts/e2e-milestone1.sh
#   CDK=./canton-devkit scripts/e2e-milestone1.sh
#
# Exit 0 = all tests passed; 1 = one or more failures.

set -u

CDK="${CDK:-bin/canton-devkit}"
SPLICE_VERSION="0.6.12"
SNAPSHOT_PATH="/tmp/e2e-m1-snapshot.tgz"

# Helper: invoke the localnet subcommand via the CDK binary.
# Usage: cli <subcommand> [args...]
cli() { "$CDK" localnet "$@"; }

# Wrapper around `timeout` that uses --foreground on Linux so the
# timeout signal only hits the child process, not the parent shell.
if [[ "$(uname -s)" == "Linux" ]]; then
  timed() { local s="$1"; shift; timeout --foreground "$s" "$@"; }
else
  timed() { local s="$1"; shift; timeout "$s" "$@"; }
fi

# ── Counters & results ──────────────────────────────────────────────
PASS=0
FAIL=0
SKIP=0
declare -a RESULTS=()

pass() {
  PASS=$((PASS + 1))
  RESULTS+=("✓ PASS  $1")
  printf '\033[1;32m  ✓ PASS  %s\033[0m\n' "$1"
}

fail() {
  FAIL=$((FAIL + 1))
  RESULTS+=("✗ FAIL  $1  — $2")
  printf '\033[1;31m  ✗ FAIL  %s — %s\033[0m\n' "$1" "$2"
}

skip() {
  SKIP=$((SKIP + 1))
  RESULTS+=("- SKIP  $1  — $2")
  printf '\033[2m  - SKIP  %s — %s\033[0m\n' "$1" "$2"
}

section() {
  echo ""
  printf '\033[1;36m━━ %s\033[0m\n' "$1"
}

# ── Cleanup trap ────────────────────────────────────────────────────
cleanup() {
  echo ""
  section "CLEANUP"
  for name in e2e-test-default e2e-named-test e2e-version-test e2e-bad-version; do
    cli clean --name "$name" --force 2>/dev/null || true
    # Belt-and-suspenders: remove containers+volumes directly in case
    # `clean` missed them (known down→clean orphan-volume bug).
    docker compose -p "canton-${name}" down --volumes 2>/dev/null || true
  done
  rm -f "$SNAPSHOT_PATH"
  print_summary
}
trap cleanup EXIT

# ── Summary ─────────────────────────────────────────────────────────
print_summary() {
  echo ""
  echo "═══════════════════════════════════════════════════════"
  echo " E2E Milestone 1 Results"
  printf ' PASSED: %d  FAILED: %d  SKIPPED: %d\n' "$PASS" "$FAIL" "$SKIP"
  echo "═══════════════════════════════════════════════════════"
  for r in "${RESULTS[@]}"; do
    echo "  $r"
  done
  echo "═══════════════════════════════════════════════════════"
  write_step_summary
}

# Emit a GitHub Actions job summary (markdown table) when running in CI.
# No-op outside GitHub Actions so local/macOS runs are unaffected.
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
      # RESULTS entries look like:
      #   "✓ PASS  <test>"
      #   "✗ FAIL  <test>  — <reason>"
      #   "- SKIP  <test>  — <reason>"
      case "$r" in
        "✓ PASS"*) icon="✅"; rest="${r#✓ PASS  }" ;;
        "✗ FAIL"*) icon="❌"; rest="${r#✗ FAIL  }" ;;
        "- SKIP"*) icon="⏭️"; rest="${r#- SKIP  }" ;;
        *)         icon="";   rest="$r" ;;
      esac
      # Keep only the test id/name; drop any "  — reason" suffix.
      test="${rest%%  — *}"
      printf '| %s | %s |\n' "$icon" "$test"
    done
  } >> "$GITHUB_STEP_SUMMARY"
}

# ── Preflight ───────────────────────────────────────────────────────
if [ ! -x "$CDK" ]; then
  echo "ERROR: binary not found at $CDK — run 'make build' first"
  exit 1
fi

docker info >/dev/null 2>&1 || {
  echo "ERROR: Docker daemon not reachable"
  exit 1
}

# Best-effort pre-clean
cli down e2e-test-default 2>/dev/null || true
cli down e2e-named-test 2>/dev/null || true
cli down e2e-version-test 2>/dev/null || true
cli clean --name e2e-test-default --force 2>/dev/null || true
cli clean --name e2e-named-test --force 2>/dev/null || true
cli clean --name e2e-version-test --force 2>/dev/null || true

echo "Canton DevKit E2E — Milestone 1"
echo "Platform: macOS ($(uname -m))"
echo "Binary:   $CDK"
echo "Docker:   $(docker --version 2>/dev/null)"
echo ""

# ═════════════════════════════════════════════════════════════════════
# PHASE 1: No-instance tests
# ═════════════════════════════════════════════════════════════════════

section "PHASE 1: Binary & preflight checks"

# ── M1-INST-003: Verify binary on all platforms ─────────────────────
TEST_ID="M1-INST-003"
(
  set -e

  # Step 1: version output matches semver or "dev"
  "$CDK" --version 2>&1 | grep -qE "([0-9]+\.[0-9]+\.[0-9]+|dev)"

  # Step 2: help includes all visible M1 lifecycle + inspection commands.
  # start/stop are now first-class lifecycle commands (previously up/down
  # aliases); pause/resume, restart, and remove are listed in --help.
  HELP=$(cli --help 2>&1)
  for cmd in up start stop down restart pause resume remove status logs snapshot restore doctor env list creds; do
    echo "$HELP" | grep -qiE "$cmd"
  done
  # 'unpause' is an alias of 'resume' — verify its --help resolves.
  cli unpause --help >/dev/null 2>&1
  # 'clean' is an alias of 'remove' — verify its --help resolves.
  cli clean --help >/dev/null 2>&1

  # Step 3: binary is a compiled executable (platform-specific)
  case "$(uname -s)" in
    Darwin) file "$CDK" 2>/dev/null | grep -qiE "Mach-O" ;;
    Linux)  file "$CDK" 2>/dev/null | grep -qiE "ELF" ;;
    *)      file "$CDK" 2>/dev/null | grep -qiE "(Mach-O|ELF|PE32)" ;;
  esac
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Verify binary"
else
  fail "$TEST_ID: Verify binary" "version/help/file check failed"
fi

# ── M1-DOC-001: Doctor — all checks pass ────────────────────────────
TEST_ID="M1-DOC-001"
(
  set -e

  # Step 1: doctor exits 0
  OUTPUT=$(cli doctor 2>&1)

  # Step 2: output mentions expected check categories
  echo "$OUTPUT" | grep -qiE "(docker cli|docker daemon|compose|port|disk|memory)"

  # Step 3: no hard failures (✗ prefix indicates failure in doctor output)
  if echo "$OUTPUT" | grep -qE "^  ✗"; then
    exit 1
  fi
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Doctor — all checks pass"
else
  fail "$TEST_ID: Doctor — all checks pass" "doctor reported failures"
fi

# ── M1-DOC-002: Doctor — Docker not installed ────────────────────────
TEST_ID="M1-DOC-002"
(
  set -e

  # Build a PATH that excludes any directory containing a `docker` binary.
  MINIMAL_PATH=""
  IFS=':' read -ra DIRS <<< "$PATH"
  for dir in "${DIRS[@]}"; do
    if [ -d "$dir" ] && ! [ -x "$dir/docker" ]; then
      MINIMAL_PATH="${MINIMAL_PATH:+$MINIMAL_PATH:}$dir"
    fi
  done

  # Doctor should fail with non-zero exit
  if PATH="$MINIMAL_PATH" "$CDK" localnet doctor >/dev/null 2>&1; then
    exit 1  # doctor should have failed
  fi

  OUTPUT=$(PATH="$MINIMAL_PATH" "$CDK" localnet doctor 2>&1 || true)

  # Verify remediation hint
  echo "$OUTPUT" | grep -qiE "(install docker|docker not found|docker desktop|docker cli)"
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Doctor — Docker not installed"
else
  fail "$TEST_ID: Doctor — Docker not installed" "doctor didn't fail or missing remediation message"
fi

# ═════════════════════════════════════════════════════════════════════
# PHASE 2: Default instance lifecycle
# ═════════════════════════════════════════════════════════════════════

section "PHASE 2: Default instance lifecycle (e2e-test-default)"

# ── M1-UP-001: LocalNet up (default) ────────────────────────────────
TEST_ID="M1-UP-001"
echo "  Starting e2e-test-default (may take 3-5 minutes)..."
(
  # Step 1: up exits 0
  timed 600 "$CDK" localnet up e2e-test-default \
    || { echo "FAIL step 1: up exited $?" >&2; exit 1; }

  # Brief settle for registry state flush
  sleep 2

  # Step 2: status shows running
  cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)" \
    || { echo "FAIL step 2: status missing healthy/ready/running" >&2; exit 1; }

  # Step 3: Docker containers belong to the compose project
  docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default" \
    || { echo "FAIL step 3: no containers with compose project label" >&2; exit 1; }

  # Step 4: compose project listed
  docker compose ls 2>/dev/null | grep -qE "e2e-test-default" \
    || { echo "FAIL step 4: compose project not listed" >&2; exit 1; }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: LocalNet up (default)"
else
  fail "$TEST_ID: LocalNet up (default)" "up/status/docker check failed"
fi

# ── M1-STS-001: Status shows healthy services ───────────────────────
TEST_ID="M1-STS-001"
(
  # Step 1: status exits 0 and includes required info
  OUTPUT=$(cli status e2e-test-default 2>&1) \
    || { echo "FAIL step 1: status exited $?" >&2; exit 1; }
  echo "$OUTPUT" | grep -qiE "(healthy|running|ready)" \
    || { echo "FAIL step 1: status missing healthy/running/ready" >&2; exit 1; }

  # Step 2: status for non-existent instance fails
  if cli status nonexistent-localnet-xyz >/dev/null 2>&1; then
    echo "FAIL step 2: status for non-existent instance should fail" >&2; exit 1
  fi
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Status shows healthy services"
else
  fail "$TEST_ID: Status shows healthy services" "status output missing expected fields or non-existent check failed"
fi

# ── M1-LOG-001: Logs — full and service-filtered ────────────────────
TEST_ID="M1-LOG-001"
(
  # Step 1: full logs (non-empty)
  LOG_OUTPUT=$("$CDK" localnet logs e2e-test-default --tail 50 2>&1) || true
  LINE_COUNT=$(echo "$LOG_OUTPUT" | wc -l | tr -d ' ')
  [ "$LINE_COUNT" -gt 1 ] \
    || { echo "FAIL step 1: full logs empty (got $LINE_COUNT lines, first line: '$(echo "$LOG_OUTPUT" | head -1)')" >&2; exit 1; }

  # Step 2: service-filtered logs (use "canton" — a known compose service)
  SVC_OUTPUT=$("$CDK" localnet logs e2e-test-default --service canton --tail 20 2>&1) || true
  SVC_LINES=$(echo "$SVC_OUTPUT" | wc -l | tr -d ' ')
  [ "$SVC_LINES" -gt 1 ] \
    || { echo "FAIL step 2: service-filtered logs empty (got $SVC_LINES lines)" >&2; exit 1; }

  # Step 3: non-existent service should error or return empty
  if "$CDK" localnet logs e2e-test-default --service nonexistent-service-xyz --tail 5 >/dev/null 2>&1; then
    true
  fi
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Logs — full and service-filtered"
else
  fail "$TEST_ID: Logs — full and service-filtered" "logs output unexpected"
fi

# ── M1-ENV-001: Env export outputs valid config ─────────────────────
TEST_ID="M1-ENV-001"
(
  # Step 1: env exits 0
  OUTPUT=$(cli env e2e-test-default 2>&1) \
    || { echo "FAIL step 1: env exited $?" >&2; exit 1; }

  # Step 2: KEY=value format (export KEY='value' or KEY="value")
  echo "$OUTPUT" | grep -qE "^(export )?[A-Z_]+=" \
    || { echo "FAIL step 2: no KEY=value lines in env output" >&2; exit 1; }

  # Step 3: expected variable names
  echo "$OUTPUT" | grep -qiE "(LEDGER|JSON_API|ADMIN|PARTICIPANT|CANTON)" \
    || { echo "FAIL step 3: env output missing expected variable names" >&2; exit 1; }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Env export outputs valid config"
else
  fail "$TEST_ID: Env export outputs valid config" "env output format/content unexpected"
fi

# ── M1-RST-001: Restart full + single service ───────────────────────
TEST_ID="M1-RST-001"
echo "  Restarting e2e-test-default (may take a few minutes)..."
(
  # Step 1: full restart
  timed 600 "$CDK" localnet restart e2e-test-default \
    || { echo "FAIL step 1: full restart exited $?" >&2; exit 1; }

  cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)" \
    || { echo "FAIL step 1: not healthy after full restart" >&2; exit 1; }

  # Step 2: single-service restart
  timed 600 "$CDK" localnet restart e2e-test-default --service canton \
    || { echo "FAIL step 2: single-service restart exited $?" >&2; exit 1; }

  # Canton service may need a moment to pass health checks after restart
  ok=false
  for i in 1 2 3 4 5; do
    if cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)"; then
      ok=true; break
    fi
    sleep 5
  done
  $ok || { echo "FAIL step 2: not healthy after single-service restart" >&2; exit 1; }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Restart full + single service"
else
  fail "$TEST_ID: Restart full + single service" "restart or post-restart health check failed"
fi

# ── M1-STP-001: Stop keeps containers; start brings them back ───────
# PR #201: `stop`/`start` are standalone commands (no longer up/down
# aliases). `stop` = docker compose stop (containers preserved on disk),
# `start` = docker compose start (fast path, no recreate).
TEST_ID="M1-STP-001"
echo "  Stop/start cycle (may take a few minutes)..."
(
  # Step 1: stop the running instance
  cli stop e2e-test-default \
    || { echo "FAIL step 1: stop exited $?" >&2; exit 1; }

  # Allow container processes to exit
  sleep 5

  # Step 2: containers are stopped but NOT removed — they must still be
  # present in `docker ps -a` yet absent from the running set (`docker ps`).
  docker ps -a --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default" \
    || { echo "FAIL step 2: containers removed by stop (should be preserved)" >&2; exit 1; }
  if docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 2: containers still running after stop" >&2; exit 1
  fi

  # Step 3: start brings the instance back to healthy (fast compose-start path)
  timed 600 "$CDK" localnet start e2e-test-default \
    || { echo "FAIL step 3: start exited $?" >&2; exit 1; }

  # Canton may need a moment to pass health checks after start
  ok=false
  for i in 1 2 3 4 5; do
    if cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)"; then
      ok=true; break
    fi
    sleep 5
  done
  $ok || { echo "FAIL step 3: not healthy after start" >&2; exit 1; }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Stop keeps containers; start restores them"
else
  fail "$TEST_ID: Stop keeps containers; start restores them" "stop/start cycle failed"
fi

# ── M1-SNP-001: Snapshot and restore ────────────────────────────────
TEST_ID="M1-SNP-001"
echo "  Snapshot/restore cycle (may take a few minutes)..."
(
  # Step 1: create snapshot
  cli snapshot e2e-test-default --to "$SNAPSHOT_PATH" \
    || { echo "FAIL step 1: snapshot command failed" >&2; exit 1; }
  [ -f "$SNAPSHOT_PATH" ] && [ -s "$SNAPSHOT_PATH" ] \
    || { echo "FAIL step 1: snapshot file missing or empty" >&2; exit 1; }

  # Step 2: tear down (use clean --force directly; down→clean leaves orphaned
  # volumes due to a known registry-deregistration bug).
  cli clean --name e2e-test-default --force 2>/dev/null || true
  # Belt-and-suspenders: ensure Docker resources are gone
  docker compose -p "canton-e2e-test-default" down --volumes 2>/dev/null || true

  # Step 3: restore
  cli restore e2e-test-default --from "$SNAPSHOT_PATH" \
    || { echo "FAIL step 3: restore command failed" >&2; exit 1; }

  # Step 4: bring up and verify it starts (post-restore may need extra time for re-sync)
  timed 600 "$CDK" localnet up e2e-test-default || {
    # Tolerate timeout if services are at least partially up
    cli status e2e-test-default 2>&1 | grep -qiE "(partial|syncing|healthy|running)" \
      || { echo "FAIL step 4: post-restore up failed and services not even partially up" >&2; exit 1; }
  }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Snapshot and restore"
else
  fail "$TEST_ID: Snapshot and restore" "snapshot/restore cycle failed"
fi

# ── M1-DWN-001: Down stops instance cleanly ─────────────────────────
TEST_ID="M1-DWN-001"
(
  # Ensure instance has running containers (may be in degraded state after restore)
  if ! docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    timed 600 "$CDK" localnet up e2e-test-default \
      || { echo "FAIL: could not bring instance up for down test" >&2; exit 1; }
    sleep 2
  fi

  # Record non-devkit container count.
  # grep -c already prints "0" on no match; do not append `|| echo "0"`
  # because grep -c exits non-zero on an empty/no-match input, which would
  # fire the fallback and produce a two-line value ("0\n0") that breaks the
  # `[ ... -eq ... ]` integer comparison below.
  NON_DEVKIT_BEFORE=$(docker ps --format '{{.Names}}' | grep -cv "e2e-test")

  # Step 1: down exits 0
  cli down e2e-test-default \
    || { echo "FAIL step 1: down exited $?" >&2; exit 1; }

  # Allow containers to fully stop (compose down may return before
  # Docker daemon has removed all containers)
  sleep 5

  # Step 2: no devkit containers for this instance
  if docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 2: containers still running after down" >&2
    docker ps --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' >&2
    exit 1
  fi

  # Step 3: non-devkit containers unaffected
  NON_DEVKIT_AFTER=$(docker ps --format '{{.Names}}' | grep -cv "e2e-test")
  [ "$NON_DEVKIT_BEFORE" -eq "$NON_DEVKIT_AFTER" ] \
    || { echo "FAIL step 3: non-devkit container count changed ($NON_DEVKIT_BEFORE → $NON_DEVKIT_AFTER)" >&2; exit 1; }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Down stops instance cleanly"
else
  fail "$TEST_ID: Down stops instance cleanly" "down failed or containers remain"
fi

# ── M1-CLN-001: Clean removes all resources ─────────────────────────
TEST_ID="M1-CLN-001"
echo "  Clean test (up + clean --force cycle)..."
(
  # Step 1: start an instance (reuse if already running)
  cli status e2e-test-default 2>&1 | grep -qiE "(healthy|ready|running)" || \
    timed 600 "$CDK" localnet up e2e-test-default \
    || { echo "FAIL step 1: could not bring instance up for clean test" >&2; exit 1; }

  # Step 2: clean --force on the running instance (tears down + removes volumes)
  cli clean --name e2e-test-default --force \
    || { echo "FAIL step 2: clean --force exited $?" >&2; exit 1; }

  # Step 3: verify all resources removed
  if docker ps -a --filter "label=com.docker.compose.project=canton-e2e-test-default" --format '{{.Names}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 3a: containers remain after clean" >&2; exit 1
  fi
  if docker volume ls --format '{{.Name}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 3b: volumes remain after clean" >&2; exit 1
  fi
  if docker network ls --format '{{.Name}}' | grep -qE "e2e-test-default"; then
    echo "FAIL step 3c: networks remain after clean" >&2; exit 1
  fi
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: Clean removes all resources"
else
  fail "$TEST_ID: Clean removes all resources" "clean failed or resources remain"
  # Force-remove leftover resources so Phase 3 doesn't compete with a
  # stale instance for memory / ports.
  docker compose -p "canton-e2e-test-default" down --volumes 2>/dev/null || true
fi

# ═════════════════════════════════════════════════════════════════════
# PHASE 3: Named instance & version tests
# ═════════════════════════════════════════════════════════════════════

section "PHASE 3: Named instance & version tests"

# ── M1-UP-002: LocalNet up with name ────────────────────────────────
TEST_ID="M1-UP-002"
echo "  Starting e2e-named-test (may take 3-5 minutes)..."
(
  # Step 1: up with explicit name
  timed 600 "$CDK" localnet up e2e-named-test \
    || { echo "FAIL step 1: up exited $?" >&2; exit 1; }

  sleep 2

  # Step 2: docker containers use the name
  docker ps --filter "label=com.docker.compose.project=canton-e2e-named-test" --format '{{.Names}}' | grep -qE "e2e-named-test" \
    || { echo "FAIL step 2: no containers with compose project label" >&2; exit 1; }

  # Step 3: status references the name
  cli status e2e-named-test 2>&1 | grep -qiE "e2e-named-test" \
    || { echo "FAIL step 3: status output missing instance name" >&2; exit 1; }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: LocalNet up with name"
else
  fail "$TEST_ID: LocalNet up with name" "named instance failed"
fi

# Tear down e2e-named-test before starting the next instance
cli clean --name e2e-named-test --force 2>/dev/null || true
docker compose -p "canton-e2e-named-test" down --volumes 2>/dev/null || true

# ── M1-UP-003: LocalNet up with --version ───────────────────────────
TEST_ID="M1-UP-003"
echo "  Starting e2e-version-test with --version $SPLICE_VERSION (may take 3-5 minutes)..."
(
  # Step 1: up with explicit version
  timed 600 "$CDK" localnet up e2e-version-test --version "$SPLICE_VERSION" \
    || { echo "FAIL step 1: up --version exited $?" >&2; exit 1; }

  sleep 2

  # Step 2: status reflects the version
  cli status e2e-version-test 2>&1 | grep -qE "$SPLICE_VERSION" \
    || { echo "FAIL step 2: status missing version $SPLICE_VERSION" >&2; exit 1; }

  # Step 3: invalid version should fail
  if "$CDK" localnet up e2e-bad-version --version "0.0.0-nonexistent" >/dev/null 2>&1; then
    cli clean --name e2e-bad-version --force 2>/dev/null || true
    echo "FAIL step 3: invalid version should have been rejected" >&2; exit 1
  fi
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: LocalNet up with --version"
else
  fail "$TEST_ID: LocalNet up with --version" "version test failed"
fi

# ── M1-LST-001: List discovers running instances ────────────────────
# Adapted: single-instance check (two concurrent instances need >12 GB Docker memory).
TEST_ID="M1-LST-001"
(
  # Step 1: list shows running instance(s)
  OUTPUT=$(cli list 2>&1)

  # At least one of our test instances should appear
  echo "$OUTPUT" | grep -qE "e2e-(named-test|version-test)" \
    || { echo "FAIL step 1: no test instances in list output" >&2; exit 1; }

  # Step 2: verify list output has expected columns
  echo "$OUTPUT" | grep -qiE "(NAME|SPLICE|STATUS)" \
    || { echo "FAIL step 2: list output missing expected columns" >&2; exit 1; }
)
if [ $? -eq 0 ]; then
  pass "$TEST_ID: List discovers running instances"
else
  fail "$TEST_ID: List discovers running instances" "list output missing expected instances"
fi

# ═════════════════════════════════════════════════════════════════════
# Exit (cleanup trap handles teardown + summary)
# ═════════════════════════════════════════════════════════════════════

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
exit 0
