#!/usr/bin/env bash
# Domain helpers for the `dpm localnet` bats e2e suite.
#
# These are the DevKit-specific bits the tests need; the generic
# pass/fail/skip/summary machinery is provided by bats-core itself
# (plus bats-support/bats-assert), so only the component + project
# scaffolding lives here.
# shellcheck shell=bash

# Repo root, derived from this file's location (e2e-tests/test_helper/).
DPM_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# DPM CLI under test. The component build-upload path shells out to a
# nested `dpm build`; that nested invocation is exactly what issue #230
# regressed on.
DPM="${DPM:-dpm}"

# Name the file-based component is registered under in daml.yaml. Must
# match the command the component publishes (`localnet`); DPM keys the
# component by name, the value is irrelevant to `dpm localnet` dispatch.
COMPONENT_NAME="${COMPONENT_NAME:-canton-devkit}"

# Extra components the test project needs to actually compile Daml.
DAMLC_COMPONENT="${DAMLC_COMPONENT:-damlc:3.5.2}"
DAML_SCRIPT_COMPONENT="${DAML_SCRIPT_COMPONENT:-daml-script:3.5.2}"

# Root for all scratch this suite creates. Under the repo (.tmp/) per
# AGENTS.md — never /tmp.
DPM_SCRATCH_DIR="${DPM_SCRATCH_DIR:-${DPM_REPO_ROOT}/.tmp/e2e-dpm}"

# dpm_available succeeds when the DPM CLI is on PATH; tests use it to
# `skip` gracefully rather than hard-fail when dpm is absent.
dpm_available() {
  command -v "$DPM" >/dev/null 2>&1
}

# dpm_build_component assembles a local DevKit component directory the
# tests install via a file-based component reference ({name, path}).
# Resolving from a local path keeps the suite hermetic (no OCI registry,
# no TLS) and exercises the binary built in THIS run. Echoes the absolute
# component dir on stdout.
#   $1 = output dir (default: .tmp/e2e-dpm-component under the repo root)
dpm_build_component() {
  local out_dir="${1:-${DPM_REPO_ROOT}/.tmp/e2e-dpm-component}"
  local bin="${CDK_BIN:-${DPM_REPO_ROOT}/bin/canton-devkit}"

  if [ ! -x "$bin" ]; then
    echo "binary not found at $bin -- run 'make build' first" >&2
    return 1
  fi

  rm -rf "$out_dir"
  mkdir -p "$out_dir/bin"
  cp "$bin" "$out_dir/bin/canton-devkit"
  sed "s|@@BINARY_PATH@@|bin/canton-devkit|" \
    "${DPM_REPO_ROOT}/packaging/component.yaml.tmpl" > "$out_dir/component.yaml"
  cp "${DPM_REPO_ROOT}/LICENSE" "$out_dir/LICENSE"

  ( cd "$out_dir" && pwd )
}

# dpm_make_project scaffolds a minimal, compilable Daml project that
# references the local DevKit component under test. Echoes the created
# project directory on stdout.
#   $1 = absolute path to the DevKit component directory
dpm_make_project() {
  local component_dir="$1"
  local dir
  mkdir -p "$DPM_SCRATCH_DIR"
  dir="$(mktemp -d "${DPM_SCRATCH_DIR}/dar-XXXXXX")"

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
