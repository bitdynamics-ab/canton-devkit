---
name: canton-token-flow
description: Create and exercise CIP-0112 token flows (create, mint, transfer, burn, balance) on a Canton LocalNet. Use when the user wants to test token operations locally.
---

# Token flows (CIP-0112)

Exercise token-standard operations on LocalNet via `dpm localnet token`.
Targets CIP-0112 (Token Standard V2) as the default path. For LocalNet
testing only — not a production issuer/custodian/wallet.

## When to use
The user asks to "create a test token", "mint/transfer/burn tokens", or
"check a wallet balance" on their local network.

## Safe workflow

1. **Ensure LocalNet is up with the tokens-v2 profile** (the V2
   on-ledger surfaces need it; see `dpm localnet versions` for an
   alpha-channel Splice version that supports it):
   ```
   dpm localnet up --name dev --profile tokens-v2
   dpm localnet status --name dev
   ```

2. **Create a token instrument**. Interactive wizard in a terminal
   (`dpm localnet token create --instance dev`), or fully flag-driven
   for agents and CI:
   ```
   dpm localnet token create --instance dev --non-interactive \
     --name "Rocket Token" --symbol RTK --decimals 6 \
     --initial-supply 1000000 --issuer <issuer-party-id>
   ```

3. **Common operations** (parties are full party ids — get them from
   `dpm localnet status --name dev`):
   ```
   dpm localnet token mint --instance dev --instrument RTK --to <party-id> --amount 1000
   dpm localnet token transfer --instance dev --instrument RTK --from <sender> --to <receiver> --amount 250
   dpm localnet token burn --instance dev --instrument RTK --from <party-id> --amount 100 --yes
   dpm localnet token balance --instance dev --instrument RTK
   ```

## Guardrails
- This is a LocalNet faucet/testing surface, not production token
  infrastructure. Do not point it at MainNet.
- V2 (CIP-0112) only — V1 / CIP-0056 is not supported by this CLI.
- All operations go through the Ledger API / Registry API on the
  selected instance; verify with `token balance` after each step.
