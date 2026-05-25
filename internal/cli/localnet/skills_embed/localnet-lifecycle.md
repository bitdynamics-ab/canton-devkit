---
# SPDX-License-Identifier: Apache-2.0
name: canton-devkit-lifecycle
description: Bring a Canton LocalNet instance up, check its status, and
  cleanly stop it. Use this whenever the user asks to "spin up a ledger",
  "start canton", "boot a fresh testnet", or anything similar.
mirrors: docs/design/mockups/screens-lifecycle.jsx
---

# Canton LocalNet — lifecycle

## What this does

Spins up a single Canton LocalNet instance using `dpm localnet up`,
verifies it reached `Status: running`, and gracefully stops it on
request. The user gets a named, reproducible local Canton environment
they can point a Daml SDK or external app at.

## Vocabulary

- **Instance** — a named local ledger (`--name demo`). Multiple instances
  coexist on different ports.
- **`dpm`** — the canton-devkit CLI. Already installed; the user wouldn't
  ask for this skill if it weren't.

## Bring up

```sh
dpm localnet up --name <NAME>
```

Defaults to the latest curated Splice version. Add `--version 0.6.4`
to pin one explicitly. The command blocks until every service is
`healthy` — typically 3–5 minutes on a cold start.

**Verification (always run this next):**

```sh
dpm localnet status --name <NAME>
```

A successful bring-up shows:

- `Status: running` in the header
- Every row of the Services table green / `healthy`
- A non-empty Endpoints section (ledger-api, json-api, wallet UI, ...)

If `Status:` is anything else, see "Failure handling" below.

## Stop

```sh
# Preserves state — re-runnable.
dpm localnet down --name <NAME>

# Keep the per-instance dir for diagnosing a bad bring-up.
dpm localnet down --name <NAME> --keep-data

# Remove containers + volumes + networks (irreversible).
dpm localnet clean --name <NAME>
```

`down` is idempotent — calling it on a missing instance exits 0 with a
"Nothing to do" line. Safe to run from a script without first checking.

## Failure handling

Exit codes (the agent should branch on these, not on stderr text):

- `0` — success
- `1` — user error (bad name, missing flag, validation)
- `2` — `ExitPreflightFail` (host isn't ready — docker down, ports
  busy, insufficient RAM)
- `3` — `ExitTimeout` (interrupted mid-call; state may be partial)
- `4` — `ExitRuntimeFailure` (docker / compose failed)

**On any non-zero exit, run `dpm localnet doctor` first.** Its
structured output names the failing precondition (`Docker daemon
unreachable`, `Port 4441 in use by pid 88341`, etc.) with remediation
text the user can act on. Never try `dmesg`, `systemctl`, or
`docker ps` directly — `doctor` already inspects all of that.

## Multiple instances

```sh
dpm localnet up --name demo
dpm localnet up --name testnet
dpm localnet list
```

Each instance gets its own port block (allocator picks free ports per
instance). State lives under `~/.canton-devkit/localnet/<name>/`. Pin
a specific Splice version per instance with `--version 0.6.4`.

## What to NOT do

- **Don't `docker compose down`.** It skips the registry write and
  `dpm localnet status` will keep reporting `running` for an
  already-stopped instance.
- **Don't edit `~/.canton-devkit/localnet/<name>/state.json` by
  hand.** Use `dpm localnet env`, `dpm localnet creds`, or stop/restart
  cycle instead.
- **Don't pass `--force` to `clean` reflexively.** It removes data with
  no undo; if the user asked to "stop" they probably wanted `down`.
