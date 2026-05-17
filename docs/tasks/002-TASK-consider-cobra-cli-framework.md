# 002 - Consider Cobra CLI Framework

**Type:** Task
**Status:** ✅ Done
**Created:** 2026-04-27
**Updated:** 2026-05-17

## Goal

Evaluate whether Canton DevKit should migrate from its initial standard-library
CLI parser to Cobra once the command surface and UX requirements are clearer.

## Progress

- [x] Review command complexity after the initial CLI boilerplate is in use
- [x] Compare standard-library parsing against Cobra for help text, validation, and subcommands
- [x] Decide whether the dependency and migration cost are justified
- [x] Document the decision and implementation plan if migration is approved

## Decision

Migrate to Cobra. The benefits (auto-generated help, shell completion via
`completion` subcommand, consistent flag parsing, subcommand extensibility)
outweigh the added dependency. Implemented in BIT-104.

## Notes

- `internal/cli/cli.go` rewritten with a Cobra command tree.
- The `App` struct (injected `io.Writer`, `Run() int`) is preserved for testability.
- DPM invocation contract (`exec-args: ["localnet"]`) still works — subcommand
  stubs use `DisableFlagParsing` so unknown future flags are passed through cleanly.
- Shell completion now available via `canton-devkit completion <shell>`.
