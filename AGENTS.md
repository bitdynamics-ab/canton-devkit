# Agent Guidelines for canton-devkit

This file provides guidelines for AI agents contributing to canton-devkit.

## Project Overview

canton-devkit is a CLI tool for managing Canton LocalNet developer environments. It provides a single workflow for starting, stopping, inspecting, and cleaning up Canton LocalNet instances.

- **Language:** Go 1.22
- **CLI Framework:** Cobra
- **Module path:** `github.com/bitdynamics-ab/canton-devkit`

## Code Change Rules

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
- **Localnet subcommands** are currently stubs
- **DPM contract:** The CLI must dispatch correctly from an argv slice with no reliance on `argv[0]` or environment variables. The `TestRunIsArgvOnly` test guards this contract and must not be broken.

## CI Pipeline

- **Linux tests are required** on every PR and push to `main`
- **macOS and Windows tests can be triggered manually** to save cost
- Lint runs on ubuntu-latest only
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
