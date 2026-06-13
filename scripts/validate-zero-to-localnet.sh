#!/usr/bin/env bash
# Zero-to-LocalNet validation harness.
#
# Times a cold-start developer journey and asserts it completes within a
# budget (default 10 minutes). The target metric is "a new user reaches
# a running LocalNet in under 10 minutes" — run it yourself before a
# release, or hand it to an external reviewer.
#
# It measures the steps a brand-new user actually performs:
#   1. binary present / built
#   2. `localnet doctor` (host readiness)
#   3. `localnet up` (download on cache miss + boot + readiness wait)  ← the long pole
#   4. `localnet status` reports healthy with endpoints
#   5. teardown
#
# Usage:
#   scripts/validate-zero-to-localnet.sh                 # 600s budget
#   BUDGET_SECONDS=900 scripts/validate-zero-to-localnet.sh
#   COLD=1 scripts/validate-zero-to-localnet.sh          # clear the cache first (true cold start)
#
# Exit 0 = passed within budget; 1 = a step failed; 2 = exceeded budget.
#
# Requirements: Docker running, docker compose v2, ~8 GB Docker memory,
# network access (a cache miss downloads ~140 MB).

set -uo pipefail

BUDGET_SECONDS="${BUDGET_SECONDS:-600}"
NAME="${NAME:-validate}"
CDK="${CDK:-bin/canton-devkit}"
COLD="${COLD:-0}"

pass() { printf '\033[1;32m✓ %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m✗ %s\033[0m\n' "$*" >&2; }
info() { printf '\033[2m  %s\033[0m\n' "$*"; }

now() { date +%s; }
START=$(now)
elapsed() { echo $(( $(now) - START )); }

cleanup() { "$CDK" localnet down --name "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# --- preflight ------------------------------------------------------
docker version >/dev/null 2>&1 || { fail "Docker daemon not reachable"; exit 1; }

if [ "$COLD" = 1 ]; then
  info "COLD=1 — clearing the Splice cache for a true cold start"
  rm -rf "${HOME}/.canton-devkit/cache" 2>/dev/null || true
fi

# --- step 1: binary -------------------------------------------------
if [ ! -x "$CDK" ]; then
  info "binary not found — building"
  make build >/dev/null 2>&1 || { fail "build failed"; exit 1; }
fi
pass "binary present ($(elapsed)s)"

# --- step 2: doctor -------------------------------------------------
if "$CDK" localnet doctor >/dev/null 2>&1; then
  pass "doctor: host ready ($(elapsed)s)"
else
  info "doctor reported advisories (non-fatal) ($(elapsed)s)"
fi

# --- step 3: up (the long pole) -------------------------------------
info "bringing up '$NAME' (downloads on cache miss; this is the long pole)…"
if "$CDK" localnet up --name "$NAME" >/tmp/validate-up.log 2>&1; then
  pass "up: instance running ($(elapsed)s)"
else
  fail "up failed after $(elapsed)s — see /tmp/validate-up.log"
  tail -20 /tmp/validate-up.log >&2 || true
  exit 1
fi

# --- step 4: status -------------------------------------------------
if "$CDK" localnet status --name "$NAME" >/dev/null 2>&1; then
  pass "status: healthy with endpoints ($(elapsed)s)"
else
  fail "status did not report healthy"
  exit 1
fi

# --- verdict --------------------------------------------------------
TOTAL=$(elapsed)
echo
if [ "$TOTAL" -le "$BUDGET_SECONDS" ]; then
  pass "ZERO-TO-LOCALNET in ${TOTAL}s (budget ${BUDGET_SECONDS}s)"
  exit 0
else
  fail "exceeded budget: ${TOTAL}s > ${BUDGET_SECONDS}s"
  exit 2
fi
