#!/usr/bin/env bash
# E2E Test Suite -- Milestone 1: LocalNet Management CLI
#
# Platform:  macOS (Apple Silicon) and Linux (CI)
# CLI mode:  canton-devkit localnet (standalone -- no DPM)
#
# Skipped tests:
#   M1-INST-001  Install via DPM component (DPM excluded)
#   M1-INST-002  Install standalone binary (binary already built)
#   M1-DOC-003   Doctor -- insufficient resources (requires Docker Desktop config changes)
#   M1-ISO-001   Two named instances (resource-heavy, skipped by request)
#
# Usage:
#   scripts/e2e-milestone1.sh
#   CDK=./canton-devkit scripts/e2e-milestone1.sh
#
# Individual tests (CI jobs):
#   scripts/e2e/m1-up-001.sh
#
# Exit 0 = all tests passed; 1 = one or more failures.

set -u

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec "${ROOT_DIR}/scripts/e2e/run-all.sh"
