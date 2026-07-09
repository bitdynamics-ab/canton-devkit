# canton-devkit

[![CI](https://github.com/bitdynamics-ab/canton-devkit/actions/workflows/ci.yml/badge.svg)](https://github.com/bitdynamics-ab/canton-devkit/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/bitdynamics-ab/canton-devkit?display_name=tag&sort=semver)](https://github.com/bitdynamics-ab/canton-devkit/releases/latest)
[![License](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)

Homebrew installation: [![Homebrew Downloads](https://img.shields.io/github/downloads/bitdynamics-ab/homebrew-canton-devkit/total.svg?label=downloads)](https://github.com/bitdynamics-ab/homebrew-canton-devkit/releases)

Other installation methods: [![Other Downloads](https://img.shields.io/github/downloads/bitdynamics-ab/canton-devkit/total.svg?label=downloads)](https://github.com/bitdynamics-ab/canton-devkit/releases)

canton-devkit helps you to run a complete local [Canton](https://canton.network/)
network on your machine. You get two participant/validator nodes and 
a super-validator nodes, each with their own party
(app-user, app-provider, super-validator) and JWT token. All done through
a single command line interface with zero knowledge required for 
infrastructure, DevOps or Docker.

The only prerequisite is Docker and at least 8 GB of available memory (12 GB
recommended) and 10 GB of free disk — see the 
[installation guide](docs/getting-started.md).

## Install

Through the dpm ([Daml Package Manager](https://docs.canton.network/sdks-tools/cli-tools/dpm)), in a project's `daml.yaml`. Ensure you remove the sdk-version field from the file. For example, if your current daml.yaml file is:
```yaml
sdk-version: 3.5.2
name: daml-test-1
source: daml
init-script: Main:setup
version: 0.0.1
dependencies:
  - daml-prim
  - daml-stdlib
  - daml-script
```

You should use the following daml.yaml file:
```yaml
#sdk-version: 3.5.2
name: daml-test-1
source: daml
init-script: Main:setup
version: 0.0.1
dependencies:
  - daml-prim
  - daml-stdlib
  - daml-script
components:
  - damlc:3.5.2
  - oci://ghcr.io/bitdynamics-ab/canton-devkit:latest
```

then `dpm install package` and use it as `dpm localnet <cmd>`.

For a standalone binary, use the quick-install script:

```bash
curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/homebrew-canton-devkit/main/install.sh | sh
```

Homebrew, APT for
Debian/Ubuntu, manual platform downloads, and `go install` are also
supported — the [installation guide](docs/getting-started.md) covers each
path step by step.

Both paths ship the same binary; `dpm localnet <cmd>` and
`canton-devkit localnet <cmd>` are interchangeable everywhere below.

## Usage

```bash
canton-devkit localnet doctor
canton-devkit localnet up demo
canton-devkit localnet status demo
eval "$(canton-devkit localnet env demo)"
canton-devkit localnet down demo
```

The command surface covers the full development loop:

| Area | Commands |
|------|----------|
| Lifecycle | `up` `down` `stop` `start` `restart` `pause` `resume` `clean` `list` `status` `logs` |
| Host checks | `doctor` — the same preflight `up` runs, with remediation hints |
| App wiring | `env` `creds` — endpoints, party IDs, and JWTs for tests and CI |
| DAR management | `dar upload / list / info / download / diff / remove / build-upload / watch` |
| Ledger inspection | `contracts ls / watch` · `tx ls / replay` |
| Tokens | `token create / mint / transfer / burn / balance` |
| State | `snapshot` / `restore` — a portable `.tgz` of a network's full state |
| Versions | `versions` — pinned Splice releases, keyed by commit SHA |

Token commands support both Canton token-standard generations, routed
per instrument: [CIP-0056](https://github.com/global-synchronizer-foundation/cips/blob/main/cip-0056/cip-0056.md)
(Final — what existing assets such as Canton Coin implement) for reads
and transfers, and Token Standard V2
([CIP-0112](https://github.com/global-synchronizer-foundation/cips/blob/main/cip-0112/cip-0112.md),
approved but not yet final) for creating new instruments, which requires
an alpha Splice build (`--version token-standard-v2 --profile tokens-v2`)
— see the [tokens guide](docs/tokens.md).

Commands that produce output take `--format json`, and exit codes are
stable (`0` ok, `1` usage, `2` preflight, `3` timeout or interrupt, `4`
runtime), so the CLI drops into CI without wrapper scripts. A ready-made
GitHub Actions workflow lives in
[`examples/ci/`](examples/ci/github-actions.yml).

Multiple named instances run side by side — each gets its own compose
project, network, and ports. Ports are auto-allocated by default;
`--port-base` pins a deterministic layout when you need one.

## Web UI

`canton-devkit localnet ui` serves a local dashboard (loopback only).
CLI and Web UI expose the same operations: instance lifecycle, live
container health and logs, a contract explorer with per-party visibility,
DAR upload and inspection, metrics, and the token workspace.

## Observability

`up --profile observability` adds Prometheus and Grafana with a bundled
Canton dashboard: transaction rates, mediator latency, per-component
health. `localnet metrics` prints the headline numbers in the terminal.
See the [observability guide](docs/observability.md) and
[dashboard customization](docs/dashboard-customization.md).

## How it works

The devkit keeps a per-instance registry (compose project, allocated
ports, party credentials, Splice version) under your user config
directory. `up` resolves a pinned Splice version from the catalogue,
materialises compose
overlays for instance isolation, allocates loopback ports, signs dev
JWTs, and waits for health checks. `snapshot` captures a logical
PostgreSQL dump (`pg_dumpall`) of the instance's database plus its
registry state into a single archive that `restore` can replay on any
machine.

Telemetry is anonymous, aggregate-only, and opt-out — no paths, no
party IDs, no per-invocation rows. Disable it with
`canton-devkit telemetry off`; [docs/telemetry.md](docs/telemetry.md)
documents every counter collected and the collector you can self-host.

## Documentation

Full documentation is published at
<https://bitdynamics-ab.github.io/canton-devkit/> and lives in
[`docs/`](docs/).

**Guides:** [getting started](docs/getting-started.md) ·
[explorer](docs/explorer.md) ·
[observability](docs/observability.md) ·
[dashboard customization](docs/dashboard-customization.md) ·
[tokens](docs/tokens.md) ·
[homebrew](docs/homebrew.md)

**Reference:** [versions](docs/versions.md) ·
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
For general Canton and Daml questions, the
[Canton forum](https://forum.canton.network/) is the better venue.

## Download statistics

Release download counts for the public builds repo
([`bitdynamics-ab/homebrew-canton-devkit`](https://github.com/bitdynamics-ab/homebrew-canton-devkit/releases)),
which mirrors the release assets published here. Charts refresh daily via
[`release-stats.yml`](.github/workflows/release-stats.yml); checksum
files are excluded from the counts. Exact numbers:
[release-downloads.md](https://github.com/bitdynamics-ab/canton-devkit/blob/release-stats-data/docs/assets/release-downloads.md).

<img src="https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/release-stats-data/docs/assets/release-downloads-by-version.svg" alt="Total downloads per release over time" width="720" />

<img src="https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/release-stats-data/docs/assets/release-downloads-by-platform.svg" alt="Downloads per platform per release over time" width="720" />

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the build, test, and lint
setup and the conventions the project holds itself to: a regression test
for every fix, CLI/Web-UI feature parity, SHA-pinned CI actions.

canton-devkit builds on the work of the
[Splice](https://github.com/canton-network/splice) and
[Canton](https://github.com/digital-asset/canton) teams.

## License

[Apache 2.0](LICENSE)
