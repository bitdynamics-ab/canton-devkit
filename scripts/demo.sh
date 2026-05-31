#!/usr/bin/env bash
# canton-devkit demo — a guided tour of the LocalNet lifecycle.
#
# Walks: build → up (with readiness wait) → status → list → env → logs →
# (optional) a second isolated instance → (optional) the V2 token flow →
# teardown. Every step is the real CLI; this is what you'd type by hand.
#
# Usage:
#   scripts/demo.sh                 # single instance lifecycle tour
#   scripts/demo.sh --two-instance  # also bring up a second, isolated instance
#   scripts/demo.sh --with-tokens   # also run the V2 token flow (needs a V2 build)
#   scripts/demo.sh --keep          # don't tear down at the end (poke around)
#
# Env:
#   CDK         path to the canton-devkit binary (default: bin/canton-devkit)
#   NAME        primary instance name (default: demo)
#   VERSION     Splice version for --with-tokens (default: token-standard-v2)
#
# Requirements: Docker running, docker compose v2, ~8 GB Docker memory,
# network access on a cache miss.

set -euo pipefail

NAME="${NAME:-demo}"
NAME2="${NAME}-b"
VERSION="${VERSION:-token-standard-v2}"
CDK="${CDK:-bin/canton-devkit}"
TWO_INSTANCE=0
WITH_TOKENS=0
KEEP=0

for arg in "$@"; do
  case "$arg" in
    --two-instance) TWO_INSTANCE=1 ;;
    --with-tokens)  WITH_TOKENS=1 ;;
    --keep)         KEEP=1 ;;
    -h|--help)      grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -22; exit 0 ;;
    *) echo "unknown flag: $arg (see --help)" >&2; exit 2 ;;
  esac
done

step() { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
run()  { printf '\033[2m$ %s\033[0m\n' "$*"; "$@"; }

# --- preflight ------------------------------------------------------
docker version  >/dev/null 2>&1 || { echo "error: Docker daemon not reachable" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "error: docker compose v2 missing" >&2; exit 1; }

# --- build (skip if a binary is already present) --------------------
if [ ! -x "$CDK" ]; then
  step "Building the canton-devkit binary"
  run make build
fi

# --- teardown trap so a Ctrl-C / failure cleans up ------------------
cleanup() {
  [ "$KEEP" = 1 ] && { echo "(--keep: leaving instances running)"; return; }
  step "Tearing down"
  run "$CDK" localnet down --name "$NAME"  || true
  [ "$TWO_INSTANCE" = 1 ] && { run "$CDK" localnet down --name "$NAME2" || true; }
}
trap cleanup EXIT

# --- lifecycle tour -------------------------------------------------
step "Bring up a LocalNet (waits for readiness)"
UP_ARGS=(localnet up --name "$NAME")
[ "$WITH_TOKENS" = 1 ] && UP_ARGS=(localnet up --name "$NAME" --version "$VERSION" --profile tokens-v2)
run "$CDK" "${UP_ARGS[@]}"

step "Health + endpoints"
run "$CDK" localnet status --name "$NAME"

step "Doctor diagnostics"
run "$CDK" localnet doctor --name "$NAME" || true

step "All managed instances"
run "$CDK" localnet list

step "Export app/test config (.env style)"
run "$CDK" localnet env --name "$NAME" | head -20 || true

step "Tail a few log lines"
run "$CDK" localnet logs --name "$NAME" --tail 15 || true

# --- optional: a second, isolated instance --------------------------
if [ "$TWO_INSTANCE" = 1 ]; then
  step "Bring up a SECOND instance — isolated ports + resources"
  run "$CDK" localnet up --name "$NAME2"
  run "$CDK" localnet list
fi

# --- optional: the V2 token flow ------------------------------------
if [ "$WITH_TOKENS" = 1 ]; then
  # The participant ledger gRPC port lives in the instance registry
  # (state.json). NOTE: Splice publishes it on an ephemeral host port that
  # Docker can reassign across a restart; if this resolves stale, re-run
  # `localnet restart --name <i>` to re-capture, or pass the live port.
  EP="$(python3 - "$NAME" <<'PY' 2>/dev/null || true
import json, os, sys
name = sys.argv[1]
p = os.path.expanduser(f"~/.canton-devkit/localnet/{name}/state.json")
d = json.load(open(p))
print("localhost:%d" % d["ports"]["participant_ledger_app-user"])
PY
)"
  if [ -z "$EP" ]; then
    echo "could not resolve the participant ledger port — skipping token flow" >&2
  else
    step "Name a couple of parties"
    run "$CDK" localnet token party new alice --instance "$NAME" --endpoint "$EP" --role app-provider
    run "$CDK" localnet token party new bob   --instance "$NAME" --endpoint "$EP" --role app-user

    step "Create a native V2 instrument (auto-uploads the test-token DARs)"
    run "$CDK" localnet token create --instance "$NAME" --endpoint "$EP" --non-interactive \
      --name "Retail Token" --symbol RTK --decimals 6 --initial-supply 1000000 --issuer alice

    step "Mint, then show everyone's balances"
    run "$CDK" localnet token mint     --instance "$NAME" --endpoint "$EP" --instrument RTK --to bob --amount 1000
    run "$CDK" localnet token balances --instance "$NAME" --endpoint "$EP"

    step "Transfer (auto-accepted) then burn"
    run "$CDK" localnet token transfer --instance "$NAME" --endpoint "$EP" \
      --instrument RTK --from bob --to alice --amount 250 --auto-accept
    run "$CDK" localnet token burn     --instance "$NAME" --endpoint "$EP" --instrument RTK --from bob --amount 100
    run "$CDK" localnet token balances --instance "$NAME" --endpoint "$EP"
  fi
fi

step "Demo complete"
