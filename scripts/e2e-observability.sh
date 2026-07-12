#!/usr/bin/env bash
# E2E — shared observability stack.
#
# Validates the shared-only path end to end, including the shared Prometheus's
# host.docker.internal scrape reachability (the piece that must hold on native
# Linux before shared can become the default). Asserts that an
# --observability-mode shared instance runs NO per-instance Prometheus/Grafana,
# is actually scraped by the host-shared Prometheus, and reports shared:true.
#
# Usage: CDK=./canton-devkit scripts/e2e-observability.sh
# Exit 0 = all passed; 1 = a failure.

set -u

CDK="${CDK:-bin/canton-devkit}"
SPLICE_VERSION="${SPLICE_VERSION:-0.6.12}"
INST="e2e-obs-shared"
SHARED_PROM="canton-devkit-observability-prometheus"

cli() { "$CDK" localnet "$@"; }
if [[ "$(uname -s)" == "Linux" ]]; then
  timed() { local s="$1"; shift; timeout --foreground "$s" "$@"; }
else
  timed() { local s="$1"; shift; timeout "$s" "$@"; }
fi

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '\033[1;32m  ✓ PASS  %s\033[0m\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '\033[1;31m  ✗ FAIL  %s — %s\033[0m\n' "$1" "$2"; }

cleanup() { cli remove "$INST" --force >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== up $INST (--observability-mode shared, Splice $SPLICE_VERSION) =="
if ! timed 1200 cli up "$INST" --profile observability --observability-mode shared --version "$SPLICE_VERSION"; then
  fail "OBS-SHARED-001: bring-up" "up failed or timed out"
  exit 1
fi
pass "OBS-SHARED-001: bring-up (shared mode)"

# No per-instance overlay containers.
if docker ps --format '{{.Names}}' | grep -qE "^canton-${INST}-(prometheus|grafana)$"; then
  fail "OBS-SHARED-002: no per-instance overlay" "found canton-${INST}-prometheus/grafana"
else
  pass "OBS-SHARED-002: no per-instance Prometheus/Grafana overlay"
fi

# The shared Prometheus actually scrapes this instance (host.docker.internal reachability).
PROM_PORT="$(docker port "$SHARED_PROM" 9090 2>/dev/null | grep -oE '[0-9]+$' | head -1)"
if [[ -z "$PROM_PORT" ]]; then
  fail "OBS-SHARED-003: shared Prometheus scrape" "could not resolve $SHARED_PROM host port"
else
  scraped=false
  for _ in $(seq 1 30); do
    n="$(curl -sf -G "http://127.0.0.1:${PROM_PORT}/api/v1/query" \
          --data-urlencode "query=up{instance=\"${INST}\"}" 2>/dev/null \
        | grep -o '"metric"' | wc -l | tr -d ' ')"
    if [[ "${n:-0}" -ge 1 ]]; then scraped=true; break; fi
    sleep 3
  done
  if $scraped; then
    pass "OBS-SHARED-003: shared Prometheus scrapes $INST (host.docker.internal OK)"
  else
    fail "OBS-SHARED-003: shared Prometheus scrape" "no up{instance=$INST} series after 90s"
  fi
fi

# observability status reports the shared-stack source.
if cli observability status --name "$INST" --format json 2>/dev/null | grep -q '"shared": *true'; then
  pass "OBS-SHARED-004: observability status reports shared:true"
else
  fail "OBS-SHARED-004: observability status shared" "shared != true"
fi

echo ""
echo "== $PASS passed, $FAIL failed =="
[[ "$FAIL" -eq 0 ]]
