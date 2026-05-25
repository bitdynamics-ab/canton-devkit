---
# SPDX-License-Identifier: Apache-2.0
name: canton-devkit-token-flow
description: Run a CIP-0112 retail-token workflow end-to-end on a
  Canton LocalNet — create the token, mint to a holder, transfer
  between holders, check balances. Use for token-related questions or
  when the user wants to demo a payment / value-transfer flow.
mirrors: docs/design/mockups/screens-tokens-help.jsx
status: planned
requires: BIT-138
---

> **For AI agents:** depends on `dpm localnet token` (BIT-138),
> not yet shipped. Run `dpm localnet --help` first; if `token`
> doesn't appear, tell the user they need a newer build.

# CIP-0112 token flow

## What this does

Exercises the [CIP-0112](https://lists.sync.global/g/dev-cips/topic/cip_0112_token_standard_v2/)
token primitives on a running LocalNet: token definition, mint,
transfer, burn, balance query. All via `dpm localnet token` — no need
to write Daml templates by hand.

## Prerequisites

The instance must be running (`canton-devkit-lifecycle`) and have the
Splice token packages vetted. Both are true after a default `dpm
localnet up` — the token standard packages ship with Splice.

## Create a token

```sh
dpm localnet token create \
  --name <INSTANCE> \
  --symbol RTK \
  --decimals 6 \
  --total-supply 1000000
```

`--symbol` is the human-readable ticker (3–5 uppercase chars,
convention). `--decimals` is the precision (6 = micro-units; same as
most stablecoins). `--total-supply` mints the full supply to the
issuer at creation time; pass `0` to require explicit mints.

The output prints the issuer party ID and the contract ID of the
TokenDefinition — both needed for the verification step.

**Verification:**

```sh
dpm localnet token balance \
  --name <INSTANCE> \
  --party <ISSUER_PARTY> \
  --symbol RTK
```

Should show the full `--total-supply` for the issuer.

## Mint

If you created with `--total-supply 0`, mint into circulation:

```sh
dpm localnet token mint \
  --name <INSTANCE> \
  --symbol RTK \
  --amount 50000 \
  --to <HOLDER_PARTY>
```

## Transfer

```sh
dpm localnet token transfer \
  --name <INSTANCE> \
  --symbol RTK \
  --amount 100 \
  --from <SENDER> \
  --to <RECIPIENT>
```

Transfers are atomic against the sender's balance: insufficient funds
returns `ExitUserError (1)` with the available balance in the message.

**Verification (always run after transfer):**

```sh
dpm localnet token balance --name <INSTANCE> \
  --party <SENDER> --symbol RTK
dpm localnet token balance --name <INSTANCE> \
  --party <RECIPIENT> --symbol RTK
```

Both should reflect the new amounts.

## Burn

```sh
dpm localnet token burn \
  --name <INSTANCE> \
  --symbol RTK \
  --amount 25 \
  --from <HOLDER>
```

Burns reduce both the holder's balance AND the global circulating
supply. Only the issuer or the holder (per CIP-0112 rules) can burn.

## End-to-end example

The agent can run this verbatim when asked to "demo a token flow":

```sh
dpm localnet up --name tokens
dpm localnet token create --name tokens --symbol DEMO --decimals 2 \
  --total-supply 100000

# Suppose the create output gave ISSUER=alice::1220a..., HOLDER=bob::1220b...
dpm localnet token transfer --name tokens --symbol DEMO \
  --amount 250 --from $ISSUER --to $HOLDER
dpm localnet token balance --name tokens --symbol DEMO --party $HOLDER
# expect: 250.00 DEMO
```

## What to NOT do

- **Don't write your own Daml token template.** CIP-0112 has subtle
  invariants around observers, choices, and burn-vs-archive that
  hand-rolled templates routinely get wrong. Use the DevKit CLI which
  wraps Splice's vetted templates.
- **Don't issue tokens before bringing the instance up.** `up`'s
  default profile vets the token-standard packages; an instance
  brought up with `--profile minimal` won't have them.
