---
name: canton-devkit-inspect-contracts
description: Query the Active Contract Set and recent transactions on
  a Canton LocalNet instance. Use when the user asks "what's on the
  ledger?", "show me the contracts", "what happened in the last
  block?", or wants to debug Daml workflows.
mirrors: docs/design/mockups/screens-contracts.jsx
---

# Inspect contracts and transactions

## What this does

Reads the Active Contract Set (ACS) and transaction history for a
running LocalNet instance and prints them in a human-readable table
or JSON. Wraps the Canton Ledger API gRPC client so you don't have to
deal with proto encoding.

## ACS — what contracts exist right now

```sh
# All active contracts for a party.
dpm localnet contracts acs \
  --name <INSTANCE> \
  --party <PARTY_ID>

# Filter by template.
dpm localnet contracts acs \
  --name <INSTANCE> \
  --party <PARTY_ID> \
  --template "Iou.Iou"
```

The output has one row per contract: contract ID, template name,
JSON-encoded payload. With `--format=json` the agent gets a clean
array it can `jq` over.

**Verification:** if the user just created a contract via a script
and `acs` doesn't show it, wait 1–2 seconds and retry. The ACS is
eventually-consistent — a freshly-committed contract takes ~500ms to
appear.

## Transactions — what happened recently

```sh
# Last 20 transactions visible to a party.
dpm localnet contracts tx \
  --name <INSTANCE> \
  --party <PARTY_ID> \
  --limit 20

# Stream new transactions as they commit.
dpm localnet contracts tx \
  --name <INSTANCE> \
  --party <PARTY_ID> \
  --follow
```

Transactions in the output include the action (create/exercise/
archive), the contract IDs involved, the choice exercised (if any),
and the committed time. `--follow` blocks and prints new transactions
as they happen — Ctrl+C to stop.

## Finding a specific contract

```sh
# By contract ID — exact match.
dpm localnet contracts get \
  --name <INSTANCE> \
  --contract-id <CID>
```

Returns the full payload + the choice menu + observer/signatory party
lists. Useful when the user has a contract ID from logs and wants to
inspect it.

## Failure handling

- Exit `1`: party ID malformed or instance doesn't exist.
- Exit `2`: instance isn't running. Bring it up.
- Exit `4`: the Ledger API rejected the query — usually missing read
  permission for the requested party. The error names which party
  the JWT is for vs which one you queried.

## What to NOT do

- **Don't curl the JSON ledger API.** The DevKit CLI handles JWT auth
  for the dev shared secret AND the gRPC-vs-HTTP-JSON differences.
  Hand-rolled curl will work for trivial queries but breaks on
  streaming, on observer parties, and on transactions with hierarchy.
- **Don't try to inspect contracts by reading the participant's
  postgres directly.** Schema isn't stable between Splice versions
  and you'll miss the ACS materialisation.
