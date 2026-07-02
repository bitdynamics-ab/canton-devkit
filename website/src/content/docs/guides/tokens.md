---
title: "Tokens — Canton Token Standard V2 on LocalNet"
description: "Create, mint, transfer, and burn CIP-0112 (Token Standard V2) instruments against a live LocalNet from the CLI or the Web UI."
---

canton-devkit ships first-class tooling for the **Canton Token Standard
V2** (the CIP-0112 path) so you can create an instrument, mint/transfer/
burn holdings, fund parties, and reconcile balances against a live
LocalNet — from the CLI **or** the Web UI, by readable party alias, with
no JWTs, ports, or 130-char contract ids in your face.

> **Scope: V2 / CIP-0112 only.** This tooling targets the Token Standard
> V2 (CIP-0112) surface. V1 / CIP-0056 is **not** supported. V2 is
> currently an opt-in *alpha* track (see [the alpha caveat](#the-v2-alpha-caveat));
> it will be promoted to the default channel once V2 lands in mainline Splice.

---

## Prerequisites — bring up a V2 LocalNet

V2 needs a special Splice build (alpha protocol 35) and a profile overlay:

```bash
# list versions — the V2 entry is tagged channel: alpha
canton-devkit localnet versions

# bring up a V2-capable instance
canton-devkit localnet up --name v2 --version token-standard-v2 --profile tokens-v2

# confirm health (doctor warns if the alpha profile is missing)
canton-devkit localnet doctor --name v2
```

All token subcommands take `--instance <name>` and, for on-ledger
actions, `--endpoint <participant host:port>` (the participant ledger
gRPC port — `localnet status --name v2` prints it). Empty `--token`
auto-issues a per-role dev JWT; `--role` defaults to `app-user`.

---

## The workspace model

On LocalNet there is **no trust boundary between parties — you own all of
them** (the dev secret signs for every role). So the token tool is a
single *god-mode workspace* over the instance, not a wallet-per-party:

- **Party aliases** — `token party new bob` allocates a party and lets
  you say `--to bob` everywhere instead of pasting its id.
- **Balance matrix** — `token balances` shows every party's balance of
  every instrument in one scan.
- **Activity feed** — `token activity` reconstructs an instrument's
  mint/transfer/burn history from the ledger.
- **Faucet** — `token faucet bob 100 --instrument Amulet` funds a party
  in one auto-accepted step.

Every command lands identically on the CLI and the Web UI **Tokens**
screen (CLI ↔ UI parity).

---

## Workflow

```bash
INST=v2
EP=localhost:63340   # participant ledger port from `localnet status`

# 1. Name a couple of parties (allocates + grants rights, records alias)
canton-devkit localnet token party new alice --instance $INST --endpoint $EP --role app-provider
canton-devkit localnet token party new bob   --instance $INST --endpoint $EP --role app-user
canton-devkit localnet token party ls        --instance $INST --endpoint $EP

# 2. Create your own native V2 instrument (auto-uploads the test-token DARs)
canton-devkit localnet token create --instance $INST --endpoint $EP --non-interactive \
  --name "Retail Token" --symbol RTK --decimals 6 --initial-supply 1000000 --issuer alice

# 3. Mint supply to a party
canton-devkit localnet token mint --instance $INST --endpoint $EP \
  --instrument RTK --to bob --amount 1000

# 4. See everyone's balances at a glance
canton-devkit localnet token balances --instance $INST --endpoint $EP

# 5. Transfer (--auto-accept settles in one step on LocalNet)
canton-devkit localnet token transfer --instance $INST --endpoint $EP \
  --instrument RTK --from bob --to alice --amount 250 --auto-accept

# 6. Inspect one instrument
canton-devkit localnet token summary  --instance $INST --endpoint $EP --instrument RTK
canton-devkit localnet token activity --instance $INST --endpoint $EP --instrument RTK

# 7. Burn supply (archives the holder's holdings; returns change)
canton-devkit localnet token burn --instance $INST --endpoint $EP \
  --instrument RTK --from bob --amount 100
```

Add `--format json` to any read command (`balance`, `balances`,
`summary`, `activity`, `party ls`) for machine-readable output.

---

## Command reference

| Command | What it does |
|---|---|
| `token create` | Create an on-ledger V2 instrument (TokenRules) for an issuer. Auto-uploads the bundled `splice-test-token-v2` DARs if not vetted. `--non-interactive` for CI; otherwise a wizard. |
| `token mint` | Mint new supply to a party (`TokenRules_OfferMint`, controller = issuer). Native CIP-0112 v2 instruments only. |
| `token transfer` | Sender-initiated transfer. `--auto-accept` chains the receiver-side accept (LocalNet default convenience); `--no-wait` returns the instruction id to hand off. |
| `token transfer accept` | Receiver accepts a pending `TransferInstruction` by id. |
| `token burn` | Burn supply. The example token has no protocol burn, so this archives the holder's `Holding` contracts directly (signatory = account parties + admin, all operator-controlled on LocalNet) and returns change. |
| `token faucet <party> <amount>` | Fund a party from a well-known source, auto-accepted. `--source` overrides the default funded party. |
| `token balance` | One party's balances. |
| `token balances` | Party × instrument balance matrix (god-mode reconciliation). |
| `token summary` | Supply / holder count / holding-contract count + holder distribution for one instrument. |
| `token activity` | Mint/transfer/burn history for one instrument, reconstructed from the ledger. |
| `token party new\|ls\|rm` | Manage the party alias registry. |

---

## The V2 alpha caveat

V2 runs only on the upstream **alpha** Splice build (snapshot image on the
`-dev` ghcr repo, `initial-protocol-version=35`). Consequences to know:

- **The upstream V2 DevNet resets periodically.** The catalogue entry may
  need refreshing each release cycle — see [Splice version catalogue](../../reference/versions/).
- **Use `--profile tokens-v2`.** Selecting the alpha version without it
  brings up a stack that can't run the V2 protocol; `doctor` warns.
- **Loopback-only dev auth.** Per-role JWTs are signed with a literal
  `unsafe` dev secret. They are valid only against your local stack —
  never reuse them against DevNet/TestNet/MainNet.

---

## Amulet vs. your own token

- **Amulet** (Canton Coin) is dual-implemented (V1 + V2). The workspace
  *observes* it — balances, matrix, activity — and can transfer it via
  the off-ledger scan registry, but it has **no mint or burn** surface
  (those are governance operations). The UI gates Mint/Burn accordingly.
- **Your own `splice-test-token-v2` instrument** is fully operable:
  create → mint → transfer → burn, all on-ledger, no scan registry
  dependency (its `TokenRules` *is* the registry).

See also: [Getting started](../../getting-started/) ·
[FAQ](../localnet-lifecycle/#faq) · [troubleshooting](../../reference/troubleshooting/) ·
[versions](../../reference/versions/).
