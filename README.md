# canton-devkit

[![CI](https://github.com/bitdynamics-ab/canton-devkit/actions/workflows/ci.yml/badge.svg)](https://github.com/bitdynamics-ab/canton-devkit/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/bitdynamics-ab/canton-devkit?display_name=tag&sort=semver)](https://github.com/bitdynamics-ab/canton-devkit/releases/latest)
[![Docs](https://img.shields.io/badge/docs-current-brightgreen.svg)](https://bitdynamics-ab.github.io/canton-devkit/)
[![License](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)

[![Homebrew Downloads](https://img.shields.io/github/downloads/bitdynamics-ab/homebrew-canton-devkit/total.svg?label=homebrew%20downloads)](https://github.com/bitdynamics-ab/homebrew-canton-devkit/releases)
[![Other Downloads](https://img.shields.io/github/downloads/bitdynamics-ab/canton-devkit/total.svg?label=other%20downloads)](https://github.com/bitdynamics-ab/canton-devkit/releases)

canton-devkit runs a complete local [Canton](https://canton.network/)
network on your machine. You get two participant/validator nodes and a
super-validator node, each with its own party (app-user, app-provider,
super-validator) and JWT. Manage the stack from a single CLI or a local
Web UI.

**Website:** [https://bitdynamics-ab.github.io/canton-devkit/](https://bitdynamics-ab.github.io/canton-devkit/)

Requires Docker and Compose v2, about 8 GB of free RAM for Docker, and
about 20 GB of free disk. See the
[installation guide](https://bitdynamics-ab.github.io/canton-devkit/getting-started/)
for details.

## Quick start

```bash
canton-devkit localnet doctor
canton-devkit localnet up demo
canton-devkit localnet status demo
eval "$(canton-devkit localnet env demo)"
canton-devkit localnet down demo
```

`dpm localnet <cmd>` and `canton-devkit localnet <cmd>` are
interchangeable.

## Commands

The command surface covers the full development loop:

| Area              | Commands                                                                             |
| ----------------- | ------------------------------------------------------------------------------------ |
| Lifecycle         | `up` `down` `stop` `start` `restart` `pause` `resume` `clean` `list` `status` `logs` |
| Host checks       | `doctor` — the same preflight `up` runs, with remediation hints                      |
| App wiring        | `env` `creds` — endpoints, party IDs, and JWTs for tests and CI                      |
| DAR management    | `dar upload / list / info / download / diff / remove / build-upload / watch`         |
| Ledger inspection | `contracts ls / watch` · `tx ls / replay`                                            |
| Tokens            | `token create / mint / transfer / burn / balance`                                    |
| State             | `snapshot` / `restore` — a portable `.tgz` of a network's full state                 |
| Versions          | `versions` — pinned Splice releases, keyed by commit SHA                             |

## Features

- Instance lifecycle management
- Host preflight checks with remediation hints
- App wiring for endpoints, party IDs, and JWTs
- DAR upload, inspect, diff, and hot redeploy
- Live ledger inspection (contracts and transactions)
- Token flows for CIP-0056 and Token Standard V2 — see the
  [tokens guide](https://bitdynamics-ab.github.io/canton-devkit/guides/tokens/)
- Snapshot and restore of a network's full state
- Both CLI and Web UI are available
- Prometheus and Grafana
- Stable exit codes and `--format json` for CI; example workflow in
  [`examples/ci/`](examples/ci/github-actions.yml)
- Multiple named instances with auto-allocated or pinned ports
  (`--port-base`)

## Install

**DPM (primary):** add the DevKit OCI component to your project's
`daml.yaml` under `components`, remove the `sdk-version` field, then run
`dpm install package`. Full steps:
[Installation & Getting Started](https://bitdynamics-ab.github.io/canton-devkit/getting-started/).

**Standalone (macOS / Linux):**

```sh
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | sh
```

Release archives (macOS arm64, Linux amd64, Windows amd64),
`SHA256SUMS`, Homebrew
(`brew install bitdynamics-ab/canton-devkit/canton-devkit`), and
`go install` are documented in the same guide.

## Documentation

Canonical docs live on the website:
[https://bitdynamics-ab.github.io/canton-devkit/](https://bitdynamics-ab.github.io/canton-devkit/).

Source Markdown also lives under [`docs/`](docs/) for browsing in the
repository:

- Guides: [getting started](docs/getting-started.md) ·
  [explorer](docs/explorer.md) ·
  [observability](docs/observability.md) ·
  [dashboard customization](docs/dashboard-customization.md) ·
  [tokens](docs/tokens.md) ·
  [homebrew](docs/homebrew.md)
- Reference: [versions](docs/versions.md) ·
  [packaging](docs/packaging.md) ·
  [telemetry](docs/telemetry.md) ·
  [FAQ](docs/faq.md) ·
  [troubleshooting](docs/troubleshooting.md) ·
  [limitations](docs/limitations.md)

This is a developer tool, not a production deployment path. For
production Canton, see the official
[Canton documentation](https://docs.daml.com/canton/).

For bugs and usage questions,
[open an issue](https://github.com/bitdynamics-ab/canton-devkit/issues/new).
For general Canton and Daml questions, use the
[Canton forum](https://forum.canton.network/).

## Download statistics

Release download counts aggregated from
[`bitdynamics-ab/canton-devkit`](https://github.com/bitdynamics-ab/canton-devkit/releases)
and
[`bitdynamics-ab/homebrew-canton-devkit`](https://github.com/bitdynamics-ab/homebrew-canton-devkit/releases)
(merged by tag). Charts refresh daily via
[`release-stats.yml`](.github/workflows/release-stats.yml); checksum
files are excluded from the counts. Exact numbers:
[release-downloads.md](https://github.com/bitdynamics-ab/canton-devkit/blob/release-stats-data/docs/assets/release-downloads.md).

<img src="https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/release-stats-data/docs/assets/release-downloads-by-version.svg" alt="Total downloads per release over time" width="720" />

<img src="https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/release-stats-data/docs/assets/release-downloads-by-platform.svg" alt="All-time downloads per platform" width="720" />

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the build, test, and lint
setup and project conventions: a regression test for every fix,
CLI/Web-UI feature parity, SHA-pinned CI actions.

To work on the documentation site locally, see
[website/DEVELOPMENT.md](website/DEVELOPMENT.md).

canton-devkit builds on the work of the
[Splice](https://github.com/canton-network/splice) and
[Canton](https://github.com/digital-asset/canton) teams.

## License

[Apache 2.0](LICENSE)
