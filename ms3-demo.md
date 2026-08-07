# Milestone 3 Demo — CIP-0112 Token Standard Tooling

This is a step-by-step script for demoing the three Milestone 3 items,
all built from this branch and verified against a live LocalNet:

1. **`dpm localnet token mint`** — CLI and Web UI minting on the
   CIP-0112 (Token Standard V2) path.
2. **`dpm localnet token create`** — interactive wizard for defining a
   new token (name, symbol, decimals, initial supply), CIP-0112 as the
   default instrument type.
3. **`dpm localnet token transfer` / `burn` / `balance`** — convenience
   commands wrapping the Ledger API / Registry API for that same path.

Every command below was run end-to-end against a real `token-standard-v2`
LocalNet instance while preparing this doc (create → mint → transfer →
burn → balance reconciled correctly).

---

## 0. Build from source

```bash
# from the repo root
make frontend   # builds the Vite bundle into internal/ui/dist (needed for a real Web UI demo)
make build      # go build -> bin/canton-devkit
```

Without `make frontend`, `go build` still succeeds but embeds a
placeholder UI bundle (`dpm localnet ui` warns about it at startup) —
run `make frontend` first so the Web UI demo shows the real Tokens
screen.

The rest of this doc calls the freshly built binary directly:

```bash
BIN=./bin/canton-devkit
$BIN version
```

(If you have `dpm` on `PATH` from a separate install, make sure you're
invoking *this* build, not a stale one — `$BIN` avoids the ambiguity.)

---

## 1. Bring up a CIP-0112-capable LocalNet

The V2 (CIP-0112) instrument-creation path needs the alpha-channel
Splice build plus a profile overlay that enables the V2 protocol
config:

```bash
# see the alpha entry (channel=alpha) in the catalogue
$BIN localnet versions

# bring up a named instance on it — up warns loudly if you pick the
# alpha version without --profile tokens-v2
$BIN localnet up ms3demo --version token-standard-v2 --profile tokens-v2
```

This pulls Docker images and takes a couple of minutes the first time
(~2m30s observed). On success you get a ready banner with the instance's
web UIs, ports, and next-step suggestions.

Get the participant ledger ports:

```bash
$BIN localnet status ms3demo
```

Look under `ENDPOINTS` for `participant_ledger_app-provider` and
`participant_ledger_app-user` (each instance auto-allocates ports, so
yours will differ from the example below):

```
participant_ledger_app-provider  tcp://localhost:55003
participant_ledger_app-user      tcp://localhost:55000
```

> **Gotcha — keep issuer + holder on the same participant.** `token
> create` uploads/vets the bundled `splice-test-token-v2` DARs only on
> the participant it dials. If you mint to a party hosted on a
> *different* participant than the issuer, the ledger rejects the mint
> with `PACKAGE_SELECTION_FAILED` ("no package … consistently vetted by
> all hosting participants"). For this demo, create every party with
> `--role app-provider` and always pass the app-provider ledger
> endpoint. (This is a LocalNet vetting-topology detail, not a bug in
> the mint/create commands.)

Export the endpoint for the rest of the CLI demo:

```bash
INST=ms3demo
EP=localhost:55003   # your app-provider participant_ledger port from `status`
```

---

## 2. CLI demo

### 2a. Name a couple of parties

```bash
$BIN localnet token party new alice  --instance $INST --endpoint $EP --role app-provider
$BIN localnet token party new holder --instance $INST --endpoint $EP --role app-provider
$BIN localnet token party ls         --instance $INST --endpoint $EP
```

Aliases let every later command say `--to alice` / `--from holder`
instead of a full `party::fingerprint` id.

### 2b. `token create` — interactive wizard (CIP-0112 default)

Run without `--non-interactive` for the six-step wizard:

```bash
$BIN localnet token create --instance $INST --endpoint $EP --role app-provider
```

```
Create a new V2 token instrument. Six fields; Ctrl-C to abort.

  Instrument name: Retail Token
  Symbol: RTK
  Decimals (0..18): 6
  Initial supply (decimal): 1000000
  Issuer party id: alice

Confirm:
  name="Retail Token" symbol="RTK" decimals=6 supply=1000000 issuer=alice
  Create? [y/N]: y

Recorded V2 instrument "Retail Token" (symbol=RTK, instrument_id=RTK, issuer=alice::...).
Initial supply 1000000 with 6 decimals.
Created on-ledger: a TokenRules contract anchors this instrument for the issuer.
```

The wizard requires a real TTY. For CI / scripted demos, use the
flag-driven form instead (same underlying `token.RunCreate`, this is
what the Web UI's `POST /api/tokens` also calls):

```bash
$BIN localnet token create --instance $INST --endpoint $EP --non-interactive \
  --name "Retail Token" --symbol RTK --decimals 6 \
  --initial-supply 1000000 --issuer alice --role app-provider
```

Both paths create a real `TokenRules` contract on-ledger for a native
CIP-0112 (HoldingV2 / TransferInstructionV2) instrument — this is the
default and only instrument type `token create` produces when an
`--endpoint` is live.

### 2c. `token mint`

```bash
$BIN localnet token mint --instance $INST --endpoint $EP \
  --instrument RTK --to holder --amount 1000 --role app-provider
```

```
mint: offered: {"amount":"1000","offer_cid":"...","to":"holder::..."}
mint: accepted: {"amount":"1000","instrument":"RTK","to":"holder::..."}
```

Issuer-only (`TokenRules_OfferMint`) — the underlying Daml choice
rejects a non-issuer submitter.

### 2d. `token balance` / `balances`

```bash
$BIN localnet token balances --instance $INST --endpoint $EP
```

```
PARTY                              RTK
──────────────────────────────────────
alice                              ·
holder                             1000.0000000000
Σ total                            1000.0000000000
```

`token balance --party holder` gives the same row for a single party,
tagged `SOURCE=ledger` (a live ACS read — vs. `SOURCE=registry` if the
instance isn't reachable, a pseudo-balance fallback).

### 2e. `token transfer`

```bash
$BIN localnet token transfer --instance $INST --endpoint $EP \
  --instrument RTK --from holder --to alice --amount 250 \
  --auto-accept --role app-provider
```

```
transfer: selected: {"amount":"250", ...}
transfer: submitted: {"transfer_instruction_id":"...", ...}
transfer accepted: {"receiver":"alice::...", ...}
transfer complete: {"transfer_instruction_id":"..."}
```

`--auto-accept` chains the receiver-side `TransferInstruction` accept
onto the sender's command — convenient on LocalNet, where you hold both
parties' keys. Re-check the matrix:

```bash
$BIN localnet token balances --instance $INST --endpoint $EP
#   alice   250.0000000000
#   holder  750.0000000000
#   Σ total 1000.0000000000
```

### 2f. `token summary` / `activity` (bonus reads)

```bash
$BIN localnet token summary  --instance $INST --endpoint $EP --instrument RTK
$BIN localnet token activity --instance $INST --endpoint $EP --instrument RTK
```

`summary` gives supply / holder count / holding-contract count +
per-holder share; `activity` reconstructs the mint/transfer/burn
history from the ledger's EventLog.

### 2g. `token burn`

Irreversible — requires either an interactive `yes` confirmation or
`--yes`:

```bash
$BIN localnet token burn --instance $INST --endpoint $EP \
  --instrument RTK --from holder --amount 100 --yes --role app-provider
```

```
burn complete: {"amount":"100","change":"650","from":"holder::...","holdings_archived":1,"instrument":"RTK"}
```

Final reconciliation:

```bash
$BIN localnet token balances --instance $INST --endpoint $EP
#   alice   250.0000000000
#   holder  650.0000000000
#   Σ total 900.0000000000
```

`900 = 1000 minted − 100 burned`, matching `summary`'s live supply.

### 2h. Bonus: `token demo` one-shot

For a single-command tour (allocate issuer + holder, create, mint —
what the Web UI's "Launch demo token" button runs):

```bash
$BIN localnet token demo --instance $INST --endpoint $EP \
  --symbol DEMO --supply 500000 --role app-provider
```

---

## 3. Web UI demo

```bash
$BIN localnet ui   # http://127.0.0.1:7777/
```

Open **`http://127.0.0.1:7777/tokens?instance=ms3demo`** in a browser
(pick the instance from the header picker if you don't pass it in the
URL; the app-provider role sees the parties created above).

On the Tokens screen:

- **Instrument list** shows `RTK` and `DEMO`, tagged
  `Token Standard V2 (CIP-0112)` — confirmed via
  `GET /api/tokens?instance=ms3demo&role=app-provider`.
- **Create token** button opens the same six-field form as the CLI
  wizard → `POST /api/tokens`.
- **Launch demo token** button → one click → `POST /api/tokens/demo`
  (same as `token demo` above).
- **Mint** button per instrument (enabled only for native on-ledger V2
  instruments — gated by `generation === "v2" && on_ledger === true`) →
  `POST /api/tokens/{symbol}/mint` with `{ to, amount }`.
- **Transfer**, **Burn**, **Faucet** buttons → `POST
  /api/tokens/{symbol}/{transfer,burn,faucet}`.
- **Balance matrix** (party × instrument) and **Activity** feed on the
  same screen, backed by `GET /api/tokens/matrix` and
  `GET /api/tokens/{symbol}/activity`.

Every action here lands on the exact same Go orchestration
(`internal/localnet/token.RunX`) as its CLI counterpart — verified by
minting 50 more RTK via `curl POST /api/tokens/RTK/mint` and seeing the
CLI's `token balances` matrix pick it up immediately (`holder` went
650 → 700 supply-side after the extra mint).

---

## 4. Teardown

```bash
$BIN localnet down ms3demo     # stop + remove containers, keep instance state
$BIN localnet remove ms3demo   # full cleanup (state, containers, volumes)
```

---

## Appendix — the V2 alpha caveat

CIP-0112 / Token Standard V2 runs only on the upstream **alpha** Splice
build (non-semver tag `token-standard-v2`, protocol version 35). It's
opt-in and separate from the stable channel used by real assets like
Canton Coin (CIP-0056, V1) until V2 lands in mainline Splice. See
`docs/tokens.md` for the full command reference and
`docs/troubleshooting.md` for the mint/burn CIP-0112-only gating note.
