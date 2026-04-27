# Canton DevKit

`canton-devkit` is a command-line tool for managing Canton LocalNet developer environments.

It is intended to provide a single local workflow for starting, stopping, inspecting, and cleaning up Canton LocalNet instances.

See current Canton grant proposal for more information: https://github.com/canton-foundation/canton-dev-fund/pull/18

## Installation

Build from source:

```sh
go install github.com/bitdynamics-ab/canton-devkit/cmd/canton-devkit@latest
```

Release binaries will be published from tagged releases once release automation
is enabled in GitHub Actions.

## Requirements

- Go 1.22 or newer for local development
- Docker for image builds and future LocalNet workflows
- `golangci-lint` for local linting

## Quick Start

TBD

```sh
canton-devkit localnet up
```

```sh
canton-devkit localnet status
```

```sh
canton-devkit localnet down
```

## Usage

```sh
canton-devkit [command]
```

## Commands

### `localnet`

Manage Canton LocalNet instances.

```sh
canton-devkit localnet up
canton-devkit localnet down
canton-devkit localnet restart
canton-devkit localnet clean
canton-devkit localnet status
canton-devkit localnet logs
```

Options:

```sh
--name <name>
--version <version>
```

## Configuration

Configuration is not implemented yet. Future LocalNet commands are expected to
accept flags such as `--name` and `--version`.

## Examples

Show help:

```sh
canton-devkit --help
```

Print version information:

```sh
canton-devkit version
```

## Development

Run tests:

```sh
make test
```

Build the CLI:

```sh
make build
```

Run linting:

```sh
make lint
```

Build the Docker image:

```sh
make docker-build
```

## Releasing

Release automation runs when changes are pushed to `main`, when a tag matching
`v*` is pushed, or when the release workflow is run manually.

The release workflow builds Linux and macOS binaries for `amd64` and `arm64`,
and publishes Docker images to GHCR.

Main branch builds upload binaries as GitHub Actions artifacts and publish Docker
images tagged as `main` and `sha-<short-sha>`.

Tagged builds upload binaries to a GitHub release and publish Docker images
tagged with the release tag and `latest`.

```sh
git tag v0.1.0
git push origin v0.1.0
```

## License

MIT
