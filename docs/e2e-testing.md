# End-to-End Testing

Unit tests (`go test ./...`) cover DevKit's Go logic. End-to-end (E2E)
tests cover the parts that only a *real* invocation exercises: the DPM
component surface, shelling out to `dpm`/`daml`, Docker-backed LocalNet
lifecycle, and the artifacts DevKit produces on disk.

The repository has two E2E layers:

| Layer | Location | Framework | Scope |
|---|---|---|---|
| **Milestone 1 (LocalNet lifecycle)** | `scripts/e2e/` (`run-all.sh` + per-test `m1-*.sh`) | hand-rolled shell harness (`scripts/e2e/lib.sh`) | `up`/`down`/`status`/`logs`/`snapshot`/`restore`, multi-instance, Docker |
| **`dpm localnet` component** | `e2e-tests/` | [bats-core](https://github.com/bats-core/bats-core) | the DPM component path: `dpm localnet …`, nested `dpm build`, DAR build/upload |

New E2E tests use **bats-core**. The Milestone 1 shell suite predates
that decision and will migrate to bats over time; until then the two
layers coexist.

## Why bats-core

The `dpm localnet` suite was originally a bespoke shell harness with its
own `pass`/`fail`/`skip` counters, result table, and aggregate-vs-
standalone dispatch. bats-core replaces all of that boilerplate with a
maintained TAP-compliant runner, so tests carry only their own logic:

- `@test` blocks, one assertion group each, each in its own subprocess.
- `setup`/`teardown` (per test) and `setup_file`/`teardown_file` (once
  per file) hooks.
- The `run` helper plus `$status`/`$output`/`$lines`, and the
  [`bats-assert`](https://github.com/bats-core/bats-assert) matchers
  (`assert_success`, `refute_output --partial`, …).
- Native `skip "reason"` for graceful skips (e.g. `dpm` not installed).

## Layout

```text
e2e-tests/
  bats/                        bats-core runner            (git submodule)
  test_helper/
    bats-support/              assertion support library   (git submodule)
    bats-assert/               assertion matchers           (git submodule)
    dpm.bash                   DevKit-specific helpers
  dpm-dar-001.bats             one test file per test ID
  daml-test-contracts/         shared Daml fixtures
```

bats-core and its helper libraries are vendored as **pinned git
submodules**, so a checkout resolves the exact tested versions with no
network install at run time (consistent with how the rest of the
toolchain is pinned):

| Submodule | Path | Pin |
|---|---|---|
| bats-core | `e2e-tests/bats` | `v1.13.0` |
| bats-support | `e2e-tests/test_helper/bats-support` | `v0.3.0` |
| bats-assert | `e2e-tests/test_helper/bats-assert` | `v2.2.4` |

The DevKit-specific glue lives in `e2e-tests/test_helper/dpm.bash`:

- `dpm_available` — succeeds when the `dpm` CLI is on `PATH`; tests use
  it to `skip` gracefully rather than hard-fail.
- `dpm_build_component` — assembles a local **file-based** DevKit
  component (`bin/canton-devkit` + a rendered `component.yaml`) from the
  binary built in this run. Resolving the component from a local
  `{name, path}` reference keeps the suite hermetic — no OCI registry,
  no TLS — and exercises *this* binary, not a released one.
- `dpm_make_project` — scaffolds a minimal, compilable Daml project that
  installs the local component.

## Running locally

Prerequisites:

- Go (see `go.mod`) — the suite builds `bin/canton-devkit`.
- The [`dpm`](getting-started.md) CLI on `PATH`. If it is missing the
  suite **skips** rather than fails.
- macOS or Linux (the `dpm localnet` suite does not require Docker; the
  Milestone 1 suite does).

Run the whole `dpm localnet` suite:

```sh
make e2e-dpm
```

`make e2e-dpm` initializes the bats submodules on demand (so a fresh
clone just works) and runs every `e2e-tests/*.bats` file. To run one
file, or pass bats flags, invoke the vendored runner directly:

```sh
BATS_LIB_PATH="$PWD/e2e-tests/test_helper" \
  e2e-tests/bats/bin/bats e2e-tests/dpm-dar-001.bats
```

Useful overrides (environment variables):

| Variable | Effect |
|---|---|
| `DPM` | Path to the `dpm` CLI (default `dpm`). |
| `CDK_BIN` | Use a prebuilt `canton-devkit` binary instead of building one. |
| `DPM_SKIP_BUILD` | Skip the in-suite `make build` (CI builds once, up front). |

Scratch (built components, scaffolded projects) is written under the
repo's gitignored `.tmp/`, never `/tmp`.

## Writing a test

Each test file is `e2e-tests/<TEST-ID>.bats`, where the test ID follows
the milestone convention (`DPM-DAR-001`, `DPM-DAR-002`, …). A minimal
file:

```bash
#!/usr/bin/env bats

setup_file() {
  bats_load_library bats-support
  bats_load_library bats-assert
  load 'test_helper/dpm'

  dpm_available || skip "dpm not found on PATH (DPM=${DPM:-dpm})"

  # Build + assemble the local component once for the file.
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

@test "DPM-DAR-002: <describe the behaviour>" {
  cd "$PROJECT_DIR"
  run "$DPM" localnet dar build-upload --build-only
  assert_success
  refute_output --partial "file exists"
}
```

Guidelines:

- Put reusable DevKit logic in `dpm.bash`, not in individual tests.
- Prefer `run` + `assert_*`/`refute_*` over hand-rolled
  `out=$(...); [ ... ]` checks — the failure output is far clearer.
- Skip (don't fail) when a required tool is absent, so contributors
  without `dpm` and unrelated CI jobs stay green.
- Keep tests hermetic: build the component locally, scaffold into
  `.tmp/`, and clean up in `teardown`.

## Continuous integration

The `dpm localnet` suite runs in
[`.github/workflows/e2e-test-dpm-localnet.yml`](../.github/workflows/e2e-test-dpm-localnet.yml)
on the self-hosted Linux runner, one job per test ID (a failed case can
be re-run without replaying the suite). Checkout uses
`submodules: recursive` so the pinned bats submodules are present. The
per-test composite action
[`.github/actions/e2e-dpm-test`](../.github/actions/e2e-dpm-test/action.yml)
builds the binary once, installs a SHA-256-verified `dpm`, and runs one
`.bats` file — passing the built binary via `CDK_BIN` rather than a
cross-job artifact (all jobs share one runner).

Triggers:

- **schedule** — nightly.
- **workflow_dispatch** — manual, ad-hoc validation.
- **pull_request** — only when the PR carries the `run-e2e` label.

The Milestone 1 suite runs analogously from
[`.github/workflows/e2e-test-devkit-functions.yml`](../.github/workflows/e2e-test-devkit-functions.yml).

## Updating pinned versions

To move a vendored library to a new release, update the submodule and
commit the new gitlink:

```sh
cd e2e-tests/bats
git fetch --tags
git checkout v1.13.1        # the desired tag
cd ../..
git add e2e-tests/bats
git commit -m "test(e2e): bump bats-core to v1.13.1"
```

The recorded submodule commit is the pin; CI and `make e2e-dpm` resolve
exactly that revision.
