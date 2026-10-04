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
   dpm localnet tx ls --name dev --party alice
   ```
   Filtering by `--party` projects the transaction stream through that
   party's visibility — the same transaction looks different to
   different parties, which is the point.

4. **Replay a single transaction** by id or offset (tree shape — shows
   exercised choices, not just ACS delta):
   ```
   dpm localnet tx replay --name dev --id <update-id>
   dpm localnet tx replay --name dev --offset <int>
   dpm localnet tx replay --name dev --offset 42 --party alice --format json
   ```
   Exactly one of `--id` / `--offset` is required. Pairs well with
   `tx ls` — list to find the offset, then replay to see the events.

## Guardrails
- `--name` resolves the participant endpoint and per-role JWT from the
  registry automatically (same as the Web UI) — no `--endpoint` /
  `--token` needed. `--name` defaults to the only registered instance.
  Use `--role sv|app-provider|app-user` to read from a different
  participant (default: `app-user`).
- Visibility is always projected through an explicit (participant,
  party) pair — there is no "global ledger" view; pick the party
  whose perspective you need. With no `--party`, the query projects
  through the JWT's own act/read parties.
- For single-contract / single-transaction lookups and CSV export, use
  `dpm daml-shell` (`contract <id>`, `transaction <id>`, `active … |
  csv`) — this tool deliberately doesn't duplicate those.
- Live watch reads the Ledger API directly; no PQS dependency, so
  archived-contract history beyond the live API is out of scope.
