# LocalNet token workspace — "god-mode" token management

## Problem

Doing anything with tokens across parties on a LocalNet instance is
currently raw-protocol work. To verify one cross-party transfer in
testing we had to:

1. Allocate a party via raw `grpcurl` against PartyManagementService.
2. Grant `CanActAs`/`CanReadAs` via raw `grpcurl` against
   UserManagementService.
3. Look up the participant ledger port by hand.
4. Look up the DSO admin party by hand.
5. Capture a 130-char `TransferInstruction` contract id and paste it
   into a second command.
6. Hand-inject the Amulet instrument into `state.json` so the UI list
   would show it.

None of that is something a DevKit user should ever touch. The current
token surface treats each party as if it were an independent
custodian — which is the right model for a *production* wallet, but the
wrong model for a LocalNet sandbox.

## Core insight

**On LocalNet there is no trust boundary between parties — you own all
of them.** The `unsafe` dev secret signs for every role; the operator
can allocate parties, grant rights, and read any ACS at will. A LocalNet
token tool should be built around that fact, not fight it.

That reframes the whole surface from "a wallet per party" to "a single
god-mode workspace over the instance" where the developer can:

- refer to parties by short alias, never by fingerprinted id,
- see every party's balance of every instrument at once,
- move tokens between any two parties in one step,
- fund a fresh party instantly,

without ever thinking about JWTs, ports, rights, or contract ids.

## Proposal

Five pieces. (1) and (2) are the foundation; the rest build on the
alias registry.

### 1. Party registry with aliases

`registry.State` gains `Parties map[string]PartyRef` (alias → party id +
participant role + created_at). Populated two ways:

- **Auto-seed on `up`**: the bootstrap local parties get aliases
  `app-user`, `app-provider`, `sv` (mirroring the roles) — discovered
  via `ListKnownParties` + the role-prefix match we already do in
  `localPartiesForRole`.
- **`localnet party new <alias>`**: allocates a party
  (PartyManagementService), auto-grants the role JWT `CanActAs` +
  `CanReadAs` for it (the manual grpcurl step #2 we hit), and records
  the alias.

Everywhere a party id is accepted — `--from`, `--to`, `--party`,
`balance --party` — an alias resolves transparently via the registry.
A 90-char id still works; an alias is just sugar.

New CLI:

```
localnet party ls                  # alias → id table
localnet party new bob             # allocate + grant + record
localnet party rm bob              # forget alias (party stays on ledger)
```

### 2. Multi-party balance matrix

Because the operator can read as every registered party, the natural
view is one table — **instruments × parties → amount** — not a
single-party wallet.

```
localnet token balances            # the whole matrix
INSTRUMENT   app-user    app-provider   sv      bob
Amulet       10985.16    4220.16        9301.5  75.0
```

Implementation: iterate the registry's parties, ACS-query each with the
HoldingV2 filter (we already have `runBalanceLive` per party), pivot
into a matrix. The existing single-party `balance` stays for scripting.

Web UI: replace the per-instrument "Holdings" sub-table with this
matrix as the default Tokens view; party columns come from the alias
registry, instruments from on-chain discovery (#4).

### 3. One-shot auto-accept transfer

Two-step offer→accept is correct V2 semantics, but on LocalNet the
operator controls the receiver, so the ceremony is pure friction for
iteration. Add `--auto-accept` (default true on LocalNet, override
with `--no-auto-accept` to exercise the real two-step flow):

```
localnet token transfer alice bob 75 --instrument Amulet
# offer → capture instruction id → accept as bob, in one command
```

Builds directly on the offer/accept orchestration already shipped; just
chains them when the receiver alias is locally hosted. Falls back to the
two-step flow (print the instruction id) when the receiver isn't a
locally-controlled party.

### 4. On-chain instrument discovery

Stop relying on `registry.State.Tokens` for the instrument list. Scan
the ACS for every contract implementing `HoldingV2`, collect distinct
`instrumentId` values, and present that as the instrument set (Amulet +
anything `token create` produced). `state.Tokens` stays as the source
of human metadata (name, symbol, decimals) but no longer gates
visibility. Removes the manual-seed hack and makes the UI reflect the
ledger.

### 5. Faucet

Funding a fresh test party is the single most common dev need and is
currently impossible (mint is unsupported on Amulet). A faucet taps an
already-funded party (the SV or validator operator wallet, which holds
Amulet from LocalNet bootstrap) and transfers to the target:

```
localnet token faucet bob 100      # sv → bob, 100 Amulet, auto-accepted
```

Implemented as a transfer (#3) from a well-known funded party — no new
ledger primitive, just a convenience wrapper.

## What stays the same

- The low-level live transfer/accept/balance orchestration is the
  engine; this is all sugar + discovery on top.
- Production-shaped single-party commands remain for users who want to
  exercise real wallet semantics.
- No change to the auth model — still the `unsafe` dev secret,
  loopback-only, never reused against a real network.

## Sequencing

1. **Party registry + aliases (#1)** — foundation, unblocks everything.
2. **Balance matrix (#2)** — highest day-to-day value once aliases exist.
3. **Auto-accept (#3)** + **faucet (#5)** — small wrappers on the above.
4. **Instrument discovery (#4)** — independent; can land any time, fixes
   the UI cosmetic gap.

## Open questions

- Alias collisions / reserved names (`sv`, `app-user`) — reject or
  shadow?
- Should `party new` auto-grant on *all three* participants or just the
  role that allocated it? (Cross-participant parties need explicit
  hosting; LocalNet's default topology hosts each party on one
  participant.)
- Faucet source selection: always SV, or pick the richest funded party
  automatically?
- CLI ↔ UI parity (AGENTS.md): every piece here lands on both surfaces.
