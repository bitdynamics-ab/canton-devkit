#!/usr/bin/env bats
# DPM-DAR-001: `dpm localnet dar build-upload` inside a Daml project.
#
# Regression test for issue #230: under `dpm localnet …`, DPM injects
# DPM_RESOLUTION_FILE pointing at a temp resolution file it already
# wrote. build-upload shells out to a nested `dpm build`, which used to
# inherit that var and abort with:
#
#   open /var/folders/.../T/<n>.yaml: file exists
#   dar build-upload: build failed: exit status 1
#
# --build-only exercises the exact build step that regressed without
# needing a running LocalNet (the failure happened before any upload).

setup_file() {
  bats_load_library bats-support
  bats_load_library bats-assert
  load 'test_helper/dpm'

  dpm_available || skip "dpm not found on PATH (DPM=${DPM:-dpm})"

  # Build the binary (unless a prebuilt one is provided via CDK_BIN or
  # DPM_SKIP_BUILD, e.g. CI builds once outside bats) and assemble the
  # local component once for the file.
  if [ -z "${DPM_SKIP_BUILD:-}" ] && [ -z "${CDK_BIN:-}" ]; then
    make -C "$DPM_REPO_ROOT" build >&2
  fi
  COMPONENT_DIR="$(dpm_build_component)"
  export COMPONENT_DIR
}

setup() {
  bats_load_library bats-support
  bats_load_library bats-assert
  load 'test_helper/dpm'

  dpm_available || skip "dpm not found on PATH (DPM=${DPM:-dpm})"

  PROJECT_DIR="$(dpm_make_project "$COMPONENT_DIR")"
}

teardown() {
  [ -n "${PROJECT_DIR:-}" ] && rm -rf "$PROJECT_DIR"
}

@test "DPM-DAR-001: build-upload inside a Daml project (issue #230)" {
  cd "$PROJECT_DIR"

  run "$DPM" install package
  assert_success

  # The failing command from issue #230. --build-only skips the upload
  # RPC so no LocalNet is required.
  run "$DPM" localnet dar build-upload --build-only
  assert_success

  # The specific regression signature must be absent: DPM_RESOLUTION_FILE
  # leaking into the nested build printed "open <path>: file exists".
  refute_output --partial "file exists"

  # A DAR must have been produced.
  run ls "${PROJECT_DIR}/.daml/dist/"
  assert_output --partial ".dar"
}
