#!/usr/bin/env bash
# Run the integration test locally - same target CI runs on
# ubuntu-latest + windows-latest. Useful on macOS where GitHub's
# runners don't ship Docker.
#
# Requirements:
#   - Docker daemon running
#   - Network access to github.com (cache miss = ~140 MB download)
#   - ~10 GB free disk + 4 GB Docker memory
#
# Usage:
#   scripts/run-integration.sh
#
# Honours the standard go test flags via $GO_TEST_FLAGS, e.g.
#   GO_TEST_FLAGS="-v -run TestIntegrationUp" scripts/run-integration.sh

set -euo pipefail

if ! docker version >/dev/null 2>&1; then
  echo "error: Docker daemon not reachable" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "error: docker compose v2 plugin missing" >&2
  exit 1
fi

# Build the binary first so a stale binary from a previous run doesn't
# mask test results.
go build -o /tmp/canton-devkit-it ./cmd/canton-devkit
trap 'rm -f /tmp/canton-devkit-it' EXIT

# shellcheck disable=SC2086
go test -tags=integration -timeout=40m -v ${GO_TEST_FLAGS:-} ./internal/localnet/...
