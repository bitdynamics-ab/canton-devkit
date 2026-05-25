---
# SPDX-License-Identifier: Apache-2.0
name: canton-devkit-ci
description: Run an ephemeral Canton LocalNet inside a CI job — bring
  up, run tests, tear down — without leaving state behind. Use when
  the user asks to "add Canton to my CI", "test against a real
  ledger in CI", or shows a GitHub Actions / GitLab CI file.
mirrors: docs/design/mockups/screens-lifecycle.jsx
---

# Ephemeral CI LocalNet

## What this does

Sets up a minimal canton-devkit invocation suitable for a CI runner:
deterministic name, fast bring-up, clean teardown on failure. The
goal is a single GitHub Actions / GitLab CI / CircleCI step the user
can copy into their pipeline.

## The skeleton

```sh
set -euo pipefail

INSTANCE_NAME="ci-${GITHUB_RUN_ID:-$$}"  # or $CI_JOB_ID, etc.

cleanup() {
  dpm localnet clean --name "$INSTANCE_NAME" --force || true
}
trap cleanup EXIT

dpm localnet doctor                         # fail fast on a bad runner
dpm localnet up --name "$INSTANCE_NAME"     # blocks until healthy
# ... your test suite, daml script, etc. ...
# Trap handles teardown.
```

Three things matter, in order of subtlety:

1. **`trap cleanup EXIT`** runs on success AND failure AND signal.
   Without it, a failed test leaves containers and volumes around
   that the next CI job inherits. `clean --force` is idempotent
   and silent on a missing instance.
2. **`dpm localnet doctor` first.** GitHub-hosted runners
   occasionally ship with broken Docker; doctor surfaces that as
   exit `2` in 5 seconds instead of timing out 15 minutes into the
   bring-up.
3. **Unique `--name`.** Use a CI-provided unique identifier
   (`GITHUB_RUN_ID`, `CI_JOB_ID`, `BUILDKITE_JOB_ID`, or `$$` for
   a local fallback). Two CI jobs on the same runner trying to use
   the same name will both fail at the registry lock.

## GitHub Actions example

```yaml
- name: Run Daml tests against Canton LocalNet
  run: |
    set -euo pipefail
    INSTANCE="ci-${GITHUB_RUN_ID}"
    trap 'dpm localnet clean --name "$INSTANCE" --force' EXIT
    dpm localnet doctor
    dpm localnet up --name "$INSTANCE"
    daml test
```

## Resource hints

`dpm localnet doctor` enforces 4 GiB RAM and 10 GiB disk minimums.
GitHub-hosted runners (`ubuntu-latest`) ship 7 GiB / 14 GiB — fine.
Larger-runners are not required for a single LocalNet.

For parallel CI jobs sharing a runner: each instance allocates a
fresh port block, so name collisions are the only sharing concern.
Use the unique-name pattern above.

## Caching the Splice fetch

Bringing up a fresh `dpm localnet` cold-pulls the Splice tarball
(~150 MB) the first time. To speed CI:

```yaml
- uses: actions/cache@v4
  with:
    path: ~/.canton-devkit/cache
    key: splice-${{ env.SPLICE_VERSION }}
```

The cache is content-addressed; mixing versions is safe.

## What to NOT do

- **Don't omit the trap.** Half the time `up` succeeds and the test
  fails, leaving containers behind. The trap catches both.
- **Don't `docker system prune` between jobs.** It nukes the Splice
  cache and you pay the 150 MB pull every job.
- **Don't `clean --force` without a name.** Bug magnet — it would
  refuse, but a mistyped `--name $UNSET_VAR` resolves to empty and
  the safety check should not be the only defence.
