# Canton DevKit

`canton-devkit` is a command-line tool for managing Canton LocalNet developer environments.

It is intended to provide a single local workflow for starting, stopping, inspecting, and cleaning up Canton LocalNet instances.

See current Canton grant proposal for more information: https://github.com/canton-foundation/canton-dev-fund/pull/18

## Installation

TBD

## Requirements

TBD

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

### `dar`

TBD

### `contracts`

TBD

### `tx`

TBD

### `metrics`

TBD

### `token`

TBD

## Configuration

TBD

## Examples

TBD

## Development

TBD

## Releasing

TBD

## License

MIT
