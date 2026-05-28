---
name: canton-inspect-contracts
description: Watch the Active Contract Set and list/replay transactions on a Canton LocalNet. Use when the user wants to see live contracts, debug transactions, or check per-party visibility.
---

# Inspect contracts & transactions

Read live ledger state with `dpm localnet contracts` and `tx` (backed
by Ledger API v2). These complement — they do not replace — Daml
Shell's one-shot lookups.

## When to use
The user asks to "watch contracts", "see transactions for a party",
"what does party X see", or "debug a privacy/authorization issue".

## Safe workflow

1. **Live-tail the ACS** (streaming creates/archives, like `kubectl -w`):
   ```
   dpm localnet contracts watch --name dev
   ```
   Filter with `--party <p>` and `--template <Module:Entity>`.

2. **List transactions** with multi-dimensional filters:
   ```
   dpm localnet tx ls --name dev --party alice --template Token:Holding
   dpm localnet tx ls --name dev --from <offset> --to <offset>
   ```

3. **Per-party visibility projection** (debug "what this party sees"):
   ```
   dpm localnet tx replay <tx-id> --name dev
   ```

## Guardrails
- Visibility is always projected through an explicit (participant,
  party) pair — there is no "global ledger" view; pick the party
  whose perspective you need.
- For single-contract / single-transaction lookups and CSV export, use
  `dpm daml-shell` (`contract <id>`, `transaction <id>`, `active … |
  csv`) — this tool deliberately doesn't duplicate those.
- Live watch reads the Ledger API directly; no PQS dependency, so
  archived-contract history beyond the live API is out of scope.
