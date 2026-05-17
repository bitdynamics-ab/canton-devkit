# 004 - Bootstrap Go Project and CI Baseline

**Type:** Task
**Status:** ✅ Complete
**Linear:** BIT-18
**Created:** 2026-05-16
**Updated:** 2026-05-16

## Goal

Initialize the DevKit Go repository CI baseline: cross-platform build/test
matrix, linter migration, README skeleton documenting the package layout and
dual-distribution model, and a contract test locking the DPM invocation
interface.

## Progress

- [x] Restructure `ci.yml` into two jobs: `test` (matrix: ubuntu, macos,
      windows) and `lint` (ubuntu-only).
- [x] Migrate from dockerized golangci-lint to
      `golangci/golangci-lint-action@v9.2.0` (SHA-pinned), pinned to
      `golangci-lint v2.12.2`.
- [x] Add *Repository Layout* and *Distribution Model* sections to README.
- [x] Add `TestRunIsArgvOnly` locking the DPM exec-args invocation contract.
- [x] Update BIT-18 acceptance criteria in Linear to reflect single-binary
      model.
- [x] PR #7 merged.

## Notes

- DPM source research (https://github.com/digital-asset/dpm) confirmed that DPM
  strips the registered command name from argv and only passes `exec-args` +
  user args to the binary. The existing single binary works for both distribution
  paths via `component.yaml` `exec-args: ["localnet"]` — no second entrypoint is
  needed.
- `TestRunIsArgvOnly` in `internal/cli/cli_test.go` locks this invariant so
  future refactors cannot silently break DPM invocation.
- Lint runs on Linux only; output is OS-independent and Docker is unavailable on
  hosted macOS/Windows runners.
- All GitHub Actions remain SHA-pinned per org policy.
- BIT-18 acceptance criteria were updated in Linear to document the single-binary
  rationale before the PR was opened.

## Related Issues

- BIT-19: Native DPM component packaging and OCI publishing.
- BIT-20: Standalone binary release pipeline.
- BIT-34: Installation and Getting Started guide.
