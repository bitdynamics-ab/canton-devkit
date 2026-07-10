# Contributing to canton-devkit

Thanks for your interest in contributing! This document describes the conventions the project follows and what we look for in a pull request.

## Project Overview

canton-devkit is a CLI tool for managing Canton LocalNet developer environments. It provides a single workflow for starting, stopping, inspecting, and cleaning up Canton LocalNet instances.

- **Language:** Go 1.26
- **CLI Framework:** Cobra
- **Module path:** `github.com/bitdynamics-ab/canton-devkit`

## Getting Started

```sh
git clone https://github.com/bitdynamics-ab/canton-devkit.git
cd canton-devkit
make build          # → ./bin/canton-devkit
make test           # run Go tests
make lint           # golangci-lint
make frontend       # build the Web UI bundle (optional)
```

### Web UI dev loop

To iterate on the `frontend/` UI, run the backend API and the Vite dev
server side by side.

**Terminal 1** — backend API + SSE (from repo root):

```sh
go run ./cmd/canton-devkit localnet ui --port 7777
```

**Terminal 2** — Vite dev server with hot reload:

```sh
cd frontend
npm install     # first run only; run `nvm use` to match .nvmrc
npm run dev
```

Open **http://localhost:5173** (not 7777). Vite proxies `/api` and
`/events` to the backend on `:7777` — see `frontend/vite.config.ts`.

Notes:

- You do **not** need `make frontend` for this loop; that target only
  builds the production bundle embedded into the Go binary. The
  placeholder-bundle warning printed at `localnet ui` startup is
  expected here since the browser loads the Vite dev server.
- Live API data requires a running LocalNet (`dpm localnet up`); pure
  UI/theming work renders without one.

For anything non-trivial, please [open an issue](https://github.com/bitdynamics-ab/canton-devkit/issues) first to discuss the change. For bugs, use the [bug report template](https://github.com/bitdynamics-ab/canton-devkit/issues/new?template=bug_report.yml).

## Code Change Rules

### CLI ↔ Web UI parity

**Any user-facing feature must land on BOTH the CLI and the Web UI surface when it applies to both.** This is a core project convention. Single-surface features are a long-term debt: an operator who learns the feature in one place can't find it in the other, and the surfaces drift in subtle ways (different validation, different error shapes, different timeouts).

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

**When you can't reach parity in the same PR**, file a follow-up issue and add a `// TODO(#issue): CLI parity — <description>` comment at the divergence point so reviewers can see it. Never close out a feature as "done" while one surface is silently missing it.

### Docker Compose teardown must be `-p`-only

**Teardown verbs (`docker compose down` / `stop` without an explicit service argument) MUST tear down by Docker project label — `-p <project>` — and MUST NOT pass `-f` compose files, `--env-file`, or `--profile`.**

Every Splice LocalNet service is profile-gated — each declares a `profiles:` list such as `sv`, `app-provider`, `app-user`, or `multi-sync`. When `-f` compose files are present, `docker compose down`/`stop` apply **profile filtering** and act only on non-profiled + explicitly-enabled-profile services — for Splice that is the **empty set**. The result is a silent no-op (exit 0) that leaves every container running and strands ledger state. This is documented compose behavior (https://docs.docker.com/compose/how-tos/profiles/#stop-application-and-services-with-specific-profiles), not a version bug, and it reproduces on every supported Compose v2.x/v5.x. Tearing down by `-p` label only is profile-agnostic and removes the whole project.

Rules:

- **Teardown (`down`/`clean`/Web-UI Stop):** use the `-p`-only path (`ComposeRunner.Stop` / `ForceStop`). Do not route teardown through `composeBase()` (which appends `-f`/`--env-file`/`--profile`). The `TestStopTeardownIsProjectLabelOnly` test pins this — do not add `-f` back.
- **Service-model subcommands (`restart`/`pause`/`unpause`/`ps`):** these genuinely need the `-f` model, so they MUST replay the enabled profile set via `composeProfiles(state)` (persisted as `state.Profiles` at `up` time, with an adapter fallback for pre-fix instances). Omitting `--profile` here targets zero services.
- **Explicitly-targeted single-service actions** (e.g. `docker compose -p <project> stop <service>`): exempt — explicitly naming a service bypasses profile filtering per the compose docs.

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
- Version is injected at build time via `-ldflags "-X main.version=$(VERSION)"`
- Output goes to `bin/`

### Code Style

- Follow standard Go conventions
- Keep packages short, helpers unexported
- Cobra command builders return `*cobra.Command`
- `App` struct uses dependency injection for `io.Writer` (testability)

## Architecture Notes

- **Entry point:** `cmd/canton-devkit/main.go`
- **CLI wiring:** `internal/cli/` — root command, version subcommand, localnet subcommands
- **Localnet subcommands** live in `internal/cli/localnet/` — run `canton-devkit localnet --help` (or check that package) for the current set.
- **DPM contract:** The CLI must dispatch correctly from an argv slice with no reliance on `argv[0]` or environment variables. The `TestRunIsArgvOnly` test guards this contract and must not be broken.

## CI Pipeline

- All CI jobs (`test`, `mockup-syntax`, `lint`, `frontend`) run on `[self-hosted, Linux]` runners on every PR and push to `main`.
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
6. **CLI ↔ Web UI parity:** if the change touches a user-facing feature, both surfaces are updated (or a follow-up issue is filed with a `TODO(#issue): CLI parity` / `TODO(#issue): UI parity` comment at the divergence point). See "CLI ↔ Web UI parity" rule above.
