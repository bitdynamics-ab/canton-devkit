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

1. **Ensure LocalNet is up and a wallet party exists**:
   ```
   dpm localnet status --name dev
   ```

2. **Create a token** (interactive wizard — name, symbol, decimals,
   initial supply, aligned with CIP-0112):
   ```
   dpm localnet token create
   ```

3. **Common operations**:
   ```
   dpm localnet token mint RTK 1000 --to alice --name dev
   dpm localnet token transfer RTK 250 --to bob --name dev
   dpm localnet token burn RTK 100 --name dev
   dpm localnet token balance RTK --name dev
   ```

## Guardrails
- This is a LocalNet faucet/testing surface, not production token
  infrastructure. Do not point it at MainNet.
- CIP-0112 (V2) is the committed default; CIP-56 (V1) support is
  optional/out-of-scope unless explicitly enabled.
- All operations go through the Ledger API / Registry API on the
  selected instance; verify with `token balance` after each step.

> Status: token commands are a Milestone 3 deliverable. If a command
> reports "not implemented yet", the network is fine — the token
> surface just isn't wired in this build.
