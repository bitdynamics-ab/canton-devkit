# Running a local Canton network with Canton DevKit

*A case study on Canton DevKit, a CLI and Web UI for local Canton and Splice networks and Canton Token Standard tokens. Built by Bit Dynamics under a Canton Dev Fund grant.*

## The problem

Local Canton development needs a network you can start, reset, and script.

Splice LocalNet provides that environment, but manual setup takes time. Each developer and CI job must repeat the same setup before it can test token logic.

Canton Token Standard V2 (CIP-0112) ships in stable Splice releases from 0.6.11. Canton DevKit removes the local network setup work so you can test a CIP-0112 flow sooner.

## What DevKit provides

Canton DevKit starts and manages Splice LocalNet from one CLI or its Web UI. You can start, stop, check, clean, snapshot, and restore local instances. A `doctor` command checks your environment before you start.

DevKit supports existing CIP-0056 assets and new CIP-0112 V2 instruments. You can use readable local names for parties instead of full Canton party IDs.

## Walkthrough

You need Docker with Compose v2. A full Splice stack needs about 8 GB of Docker memory, 12 GB recommended, and 20 GB of disk space.

Start a LocalNet that supports CIP-0112:

```bash
canton-devkit localnet up --name v2 --version 0.6.12
canton-devkit localnet status --name v2
```

The status command shows each service, its port, and participant endpoint.

`dpm localnet ...` and `canton-devkit localnet ...` run the same commands. Use the name your team installed.

Open the Web UI:

```bash
canton-devkit localnet ui
```

The UI is available at `http://127.0.0.1:7777` by default.

### Create parties

Create two local parties:

```bash
canton-devkit localnet token party new alice --instance v2 --role app-provider
canton-devkit localnet token party new bob   --instance v2 --role app-provider
```

Use `alice` and `bob` in later commands.

### Create a CIP-0112 token

Create a V2 instrument for Alice:

```bash
canton-devkit localnet token create --instance v2 --non-interactive \
  --name "Retail Token" \
  --symbol RTK \
  --decimals 6 \
  --initial-supply 1000000 \
  --issuer alice
```

### Mint and transfer

Mint RTK for Bob:

```bash
canton-devkit localnet token mint \
  --instance v2 \
  --instrument RTK \
  --to bob \
  --amount 1000
```

Transfer RTK back to Alice:

```bash
canton-devkit localnet token transfer \
  --instance v2 \
  --instrument RTK \
  --from bob \
  --to alice \
  --amount 250 \
  --auto-accept
```

`--auto-accept` completes the recipient acceptance in the same command.

Check the balances:

```bash
canton-devkit localnet token balances --instance v2
```

### Transfer between participants

Create Carol on a second participant:

```bash
canton-devkit localnet token party new carol \
  --instance v2 \
  --role app-user
```

Start a transfer to Carol:

```bash
canton-devkit localnet token transfer \
  --instance v2 \
  --role app-provider \
  --instrument RTK \
  --from alice \
  --to carol \
  --amount 100 \
  --no-wait
```

The command prints a transfer instruction ID. Accept it as Carol:

```bash
canton-devkit localnet token transfer accept \
  --instance v2 \
  --role app-user \
  --party carol \
  --id <instruction-id>
```

### Canton Coin and Amulet

DevKit can inspect and transfer supported CIP-0056 assets. The faucet can fund a party with Amulet:

```bash
canton-devkit localnet token faucet bob 100 \
  --instance v2 \
  --instrument Amulet
```

You can burn V2 test instruments that you create with DevKit:

```bash
canton-devkit localnet token burn \
  --instance v2 \
  --instrument RTK \
  --from alice \
  --amount 100
```

Burning is permanent. The command asks for confirmation. Use `--yes` in CI.

### Web UI and automation

The Tokens section of the Web UI supports party management, instrument creation, minting, transfers, transfer acceptance, burning V2 test tokens, the faucet, balances, DvP allocations, and token activity.

Use the CLI for automation and CI. Commands that read data, including balances, summaries, and activity, support `--format json`.

Clean up an instance when you finish:

```bash
canton-devkit localnet clean --name v2
```

This removes its containers, volumes, and local state.

## Who is this for?

DevKit is for Daml and Canton developers, CI pipelines, and teams that want to test CIP-0112 without building local network setup scripts.

You can run the CIP-0112 flow through the CLI or the Web UI.

Use DevKit only for local development. Do not use its local credentials with DevNet, TestNet, or MainNet.

## Current limits

DevKit supports V2 issuance and DvP allocations. You can create, list, withdraw, and cancel allocations from the CLI and UI.

Batch settlement is not yet available on LocalNet. Until it is available, you cannot complete a DvP flow end to end.

---

*Repository: github.com/bitdynamics-ab/canton-devkit. Apache 2.0. Built under a Canton Dev Fund grant. Not an official Canton or Digital Asset project.*
