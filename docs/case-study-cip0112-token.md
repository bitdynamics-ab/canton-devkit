# Boot Canton locally and issue a CIP-0112 token with Canton DevKit

*A case study on Canton DevKit — a CLI and Web UI for running a local Canton + Splice network and working with Canton Token Standard tokens. Built by Bit Dynamics under a Canton Dev Fund grant.*

## The problem

If you are building on Canton, sooner or later you need a local network you can break, reset, and script against.

For local development, the realistic option is Splice LocalNet: a Canton synchronizer and participant, the Splice super-validator, validator, name-service and Scan apps, Postgres, and the wallet and explorer UIs around them.

Upstream ships all of this as a Docker Compose project, which is the right approach. But standing it up by hand is still a chore. You clone Splice, work through the compose layers to find the pieces you need, dig out the development JWT secret, copy party IDs around, and wire everything together.

Then you do it again on another machine, in CI, or when somebody else on the team needs the same environment.

None of that is hard. It is just slow, easy to get subtly wrong, and it has nothing to do with the token logic you actually wanted to test.

The token side, at least, is no longer the hard part. Canton Token Standard V2 (CIP-0112) is an approved Canton Improvement Proposal and ships in stable Splice releases from 0.6.11 onward. There is no longer an alpha build or separate profile overlay to set up.

So the main thing between `docker` and a live CIP-0112 flow is the wiring. That is what DevKit removes.

## The approach

Canton DevKit is a single Go binary that wraps the upstream Splice LocalNet Compose project instead of forking it: it fetches `cluster/compose/localnet/` from a pinned Splice commit and verifies the extracted tree against a content hash, though the Splice images themselves still move under their upstream tags. On top of that it adds the lifecycle and workflow tooling you would otherwise script by hand — `up`, `down`, `status`, `clean`, snapshot and restore, a `doctor` preflight, DAR upload, and the token surface — with the same functionality available from both the CLI and the Web UI, backed by one shared implementation.

DevKit works with both Canton Token Standard generations: existing CIP-0056 assets such as Canton Coin can be inspected and transferred, while new instruments use the CIP-0112 V2 flow. Because LocalNet signs for every party with a fixed development secret, the token tooling treats an instance as a single workspace — you refer to parties by readable aliases, and on-ledger commands resolve the participant endpoint from the instance and role, so you rarely pass `--endpoint` yourself.

## Walkthrough

You need Docker with Compose v2, plus room for a full Splice stack — roughly 8 GB or more of Docker memory (around 12 GB recommended) and about 20 GB of disk.

Start a V2-capable LocalNet using a current stable Splice release:

```bash
canton-devkit localnet up --name v2 --version 0.6.12
canton-devkit localnet status --name v2
```

The status command shows the running services, ports and participant endpoints.

`dpm localnet …` and `canton-devkit localnet …` invoke the same binary, so use whichever form your team installed.

You can also open the Web UI:

```bash
canton-devkit localnet ui
```

By default it is available at `http://127.0.0.1:7777`. The UI binds to `127.0.0.1` and refuses a non-loopback address unless you explicitly pass `--allow-non-loopback`, which makes it harder to expose the development UI accidentally.

### Create a couple of parties

First, give two parties readable names:

```bash
canton-devkit localnet token party new alice --instance v2 --role app-provider
canton-devkit localnet token party new bob   --instance v2 --role app-provider
```

`party new` allocates the party on the participant associated with the selected role and stores the alias locally. From this point on, commands can use `alice` and `bob` instead of full Canton party IDs.

Both parties are on the `app-provider` participant for this first example. We will use a second participant shortly.

### Create a CIP-0112 token

Create a native V2 instrument:

```bash
canton-devkit localnet token create --instance v2 --non-interactive \
  --name "Retail Token" \
  --symbol RTK \
  --decimals 6 \
  --initial-supply 1000000 \
  --issuer alice
```

If the required `splice-test-token-v2` DARs have not already been vetted, DevKit uploads them as part of the flow and creates the instrument on-ledger for Alice.

### Mint and transfer

Mint some RTK to Bob:

```bash
canton-devkit localnet token mint \
  --instance v2 \
  --instrument RTK \
  --to bob \
  --amount 1000
```

Now transfer some of it back to Alice:

```bash
canton-devkit localnet token transfer \
  --instance v2 \
  --instrument RTK \
  --from bob \
  --to alice \
  --amount 250 \
  --auto-accept
```

Because Alice and Bob are both on the same participant, DevKit can immediately perform the receiver-side acceptance as part of the same command.

Check the resulting balances:

```bash
canton-devkit localnet token balances --instance v2
```

### Transfer across two participants

To see what the same flow looks like across different participants, allocate another party under `app-user`:

```bash
canton-devkit localnet token party new carol \
  --instance v2 \
  --role app-user
```

Alice is on the `app-provider` participant and Carol is on `app-user`.

Start the transfer from Alice's side:

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

`--no-wait` creates the transfer and returns the `TransferInstruction` ID without accepting it.

Carol then accepts the instruction from her own participant:

```bash
canton-devkit localnet token transfer accept \
  --instance v2 \
  --role app-user \
  --party carol \
  --id <instruction-id>
```

Now the two sides are actually going through different participants: Alice initiates the transfer from `app-provider`, and Carol accepts it from `app-user`.

### Canton Coin / Amulet

The same token tooling can also work with CIP-0056 assets where the operation is supported. For example, the faucet can fund a party with Amulet:

```bash
canton-devkit localnet token faucet bob 100 \
  --instance v2 \
  --instrument Amulet
```

Canton Coin is discovered through the Scan registry and can be inspected and transferred through the same token surface. Minting and burning Amulet are governance operations, however, so DevKit does not expose those actions.

For V2 test instruments created through DevKit, there is a burn command:

```bash
canton-devkit localnet token burn \
  --instance v2 \
  --instrument RTK \
  --from alice \
  --amount 100
```

Burn is irreversible, so the command asks for confirmation. In CI, `--yes` can be used to skip the interactive prompt.

### The Web UI

The same workflows are available from the Tokens section of the Web UI. You can create instruments, mint, transfer and accept transfers, burn V2 test tokens, use the faucet, manage parties, inspect the balance matrix, work with DvP allocations, and view per-instrument activity reconstructed from ledger events.

The CLI is still useful for automation and CI. Read-oriented commands such as balances, summaries and activity support machine-readable output using `--format json`.

Once you are finished with the environment:

```bash
canton-devkit localnet clean --name v2
```

removes the containers, volumes and LocalNet state for that instance.

## Who is this useful for?

DevKit is mainly useful for Daml and Canton developers who currently hand-roll Splice LocalNet setups, CI pipelines that need predictable machine-readable commands, and teams experimenting with CIP-0112 without wanting to assemble the surrounding LocalNet infrastructure first.

The CIP-0112 flow above can be run end-to-end through either the CLI or the Web UI.

This is strictly development tooling. LocalNet JWTs are signed using the fixed `unsafe` development secret and should only ever be used against the local environment — never DevNet, TestNet or MainNet.

## What's next

DevKit already supports the V2 issuance flow and DvP allocations. You can create allocations and list, withdraw or cancel them from both the CLI and the UI.

The main missing piece is batch settlement on LocalNet through `SettlementFactory_SettleBatch`. Once that is available, the DvP flow can run end to end.

DevKit's version catalogue follows stable Splice releases and currently defaults `latest` to 0.6.12. As new stable releases land, the catalogue is updated and tested against them.

The token CLI and Tokens UI continue to use the same underlying implementation as those capabilities are added.

---

*Repository: github.com/bitdynamics-ab/canton-devkit — Apache 2.0. Built under a Canton Dev Fund grant. Not an official Canton or Digital Asset project.*
