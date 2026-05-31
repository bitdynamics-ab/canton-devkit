# Telemetry

canton-devkit collects **anonymous, aggregate** usage data to guide
development and report ecosystem adoption (Milestone 4). This page
documents exactly what is collected, how to turn it off, and how to audit
it. You can always see precisely what's queued with:

```bash
canton-devkit localnet telemetry status
```

## On by default (opt-out)

Telemetry is **enabled by default**. The first time a command runs, a
one-time notice is printed explaining this and how to opt out. Disable it
any time — your choice persists:

```bash
canton-devkit localnet telemetry off      # disable (persists)
canton-devkit localnet telemetry on       # re-enable

# or, per-invocation / environment-wide, without writing config:
export CANTON_DEVKIT_TELEMETRY=0           # also: false / off / no
export DO_NOT_TRACK=1                       # the community standard
```

An env override always wins over the persisted setting.

## What is collected

Each record is a single, aggregate event:

| Field | Example | Why |
|---|---|---|
| `event` | `command` | event type |
| `command` | `localnet token mint` | **command path only** — never arguments or flag values |
| `exit_code` | `0` | success/failure rates |
| `duration_bucket` | `500ms-2s` | coarse timing (never a precise duration) |
| `tool_version` | `1.2.3` | which release is in use |
| `os` / `arch` | `darwin` / `arm64` | platform coverage |
| `install_id` | random 128-bit hex | de-duplicate installs; **not** derived from any machine identifier |
| `ts` | RFC3339 | when |

## What is **never** collected

This is a hard boundary enforced by the data model — there is no field
that can carry any of it:

- instance names, party ids, aliases
- DAR names, contents, or package ids
- ports, endpoints, credentials, JWTs
- file paths, working directory, home directory
- command arguments or flag values
- environment variables, IP addresses, hostnames, usernames

Only the **command path** is recorded (e.g. `localnet token mint`), so
`token mint --to alice::1220… --amount 1000` records just
`localnet token mint`.

## How it works

- Events append to a local spool (`~/.canton-devkit/telemetry-spool.jsonl`,
  capped at 500). Recording is a fast local write.
- The spool is flushed to the collector in **batches** (every ~20 events,
  or when the oldest queued event is over a day old) — never on every
  command.
- Flushes are **non-blocking-bounded**: a hard ~1.2s timeout, and any
  failure (offline, slow, error response) leaves events queued and is
  completely silent. Telemetry never slows or breaks a command.
- With **no collector endpoint configured** (the default in a source
  build), nothing ever leaves your machine — events only spool locally.
  Release binaries may bake in an endpoint; `telemetry status` always
  shows whether one is set and where.

## Audit it

```bash
canton-devkit localnet telemetry status            # state + queued events
canton-devkit localnet telemetry status --format json
```

The `status` output lists every queued event verbatim — exactly the bytes
that would be sent — so you can verify the claims on this page yourself.

See also: [FAQ](faq.md) · [getting-started](getting-started.md).
