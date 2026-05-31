# Agent Guidelines for canton-devkit

This file provides guidelines for AI agents contributing to canton-devkit.

## Project Overview

canton-devkit is a CLI tool for managing Canton LocalNet developer environments. It provides a single workflow for starting, stopping, inspecting, and cleaning up Canton LocalNet instances.

- **Language:** Go 1.26
- **CLI Framework:** Cobra
- **Module path:** `github.com/bitdynamics-ab/canton-devkit`

## Code Change Rules

### CLI ↔ Web UI parity (load-bearing)

**Any user-facing feature must land on BOTH the CLI and the Web UI surface when it applies to both.** Single-surface features are a long-term debt: an operator who learns the feature in one place can't find it in the other, and the surfaces drift in subtle ways (different validation, different error shapes, different timeouts).

When adding or changing a feature, ask:

1. **Is this a per-instance operation a user might want from either surface?** (start / stop / restart / scrub / log tail / health probe / per-container action / pre-flight check / reconcile / etc.) → wire both.
2. **Is it a pure-backend internal detail?** (registry locking, atomic-write semantics, hub topic naming) → backend only.
3. **Is it a pure-UI affordance?** (modal animations, layout, color palette) → frontend only.

If unsure, default to "both."

When the work spans both:

- **Share the data shape** via `internal/api/types/` so the CLI's `--json` flag and the Web UI's REST/SSE payload emit the same Go struct. Drift between the two shapes is what `internal/api/types/schema_pin_test.go` exists to catch.
- **Share the business logic** by extracting to a neutral package (e.g. `internal/localnet/`, `internal/docker/`, `internal/registry/`) and calling it from both surfaces. Avoid duplicating the logic in `internal/cli/localnet/*.go` and `internal/ui/handlers/*.go`.
- **Mirror the verbs.** If the UI gets `POST /api/instances/{name}/containers/{c}/restart`, the CLI should get `dpm localnet container restart <inst> <c>`. The CLI name is a wrapper around the same handler logic; both pass through the same shared function.
- **Mirror the guards.** If the Web UI's pre-flight gate refuses to start a Splice 0.6.4 instance on a 4 GiB host, `dpm localnet up --version 0.6.4` must refuse it for the same reason. Don't let one surface be lenient where the other is strict.

**When you can't reach parity in the same PR**, file a follow-up ticket and add a `// TODO(BIT-NNN): CLI parity — <description>` comment at the divergence point so reviewers can see it. Never close out a feature as "done" while one surface is silently missing it.

### Testing Requirements

- **All bug fixes must include regression tests**
- **All new code must be tested**
- Run tests before submitting: `make test`
- Testing pattern: inject `bytes.Buffer` into the `App` struct for stdout/stderr — no process-global side effects
- **Coverage goal:** Test coverage should not decrease. Check with:
  ```bash
  go test -covermode=atomic -coverprofile=coverage.out ./...
  go tool cover -func=coverage.out
  ```
  Coverage is not yet enforced in CI, but is a goal to work toward. All lines of code touched by changes should be covered by tests.

### Linting & Formatting

- Run before submitting: `make lint`
- Uses golangci-lint v2.12.2 (must match CI version)
- Linters enabled: `govet`, `ineffassign`, `staticcheck`, `unused`
- Formatters enabled: `gofmt`, `goimports`
- Pre-commit hook available: install with `uvx pre-commit install`

### Build

- Build the binary: `make build`
- Version is injected at build time via `-ldflags -X main.version=...`
- Output goes to `bin/`

### Code Style

- Follow standard Go conventions
- Keep packages short, helpers unexported
- Cobra command builders return `*cobra.Command`
- `App` struct uses dependency injection for `io.Writer` (testability)

## Architecture Notes

- **Entry point:** `cmd/canton-devkit/main.go`
- **CLI wiring:** `internal/cli/` — root command, version subcommand, localnet subcommands
- **Localnet subcommands** are partially implemented — check `internal/cli/localnet/` for the current set; commands not yet wired return a "not implemented yet" stub.
- **DPM contract:** The CLI must dispatch correctly from an argv slice with no reliance on `argv[0]` or environment variables. The `TestRunIsArgvOnly` test guards this contract and must not be broken.

## CI Pipeline

- Both jobs (`test` and `lint`) run on `[self-hosted, Linux]` runners on every PR and push to `main`.
- macOS / Windows CI was removed in commit `9a0dae1` — cross-platform validation now lives in `.github/workflows/release.yml`.
- All GitHub Actions must be SHA-pinned (no floating tags like `@v4`)

## Commit Messages

- Commit messages must explain **why** the change is needed
- Keep messages clear and informative

## PR Checklist

Before submitting:

1. Tests pass (`make test`)
2. Lint passes (`make lint`)
3. No test coverage regression (check with `go tool cover`)
4. Relevant documentation added/updated
5. PR title is clear and understandable
6. **CLI ↔ Web UI parity:** if the change touches a user-facing feature, both surfaces are updated (or a follow-up ticket is filed with a `TODO(BIT-NNN): CLI parity` / `TODO(BIT-NNN): UI parity` comment at the divergence point). See "CLI ↔ Web UI parity" rule above.
