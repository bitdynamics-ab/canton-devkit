# Telemetry

canton-devkit records **anonymous, aggregate usage counters** — merged
into a weekly total with **no per-invocation rows and no identifiers of
any kind** — to help the team see what's used and what breaks. See the
full design at [docs/proposals/telemetry.md](proposals/telemetry.md).

Inspect exactly what's queued any time:

```bash
canton-devkit telemetry preview
```

## On by default (opt-out)

Telemetry is **on by default**. The first time you run an operational
command in an interactive terminal, a one-time notice explains this. Turn
it off any time — your choice persists:

```bash
canton-devkit telemetry off      # disable (persists)
canton-devkit telemetry on       # re-enable

# or, per-invocation / environment-wide, without writing config:
export DPM_TELEMETRY=off          # also: on
export DO_NOT_TRACK=1             # the community standard — always wins
```

Precedence (highest first): `DO_NOT_TRACK` → `DPM_TELEMETRY` → config file
→ default on.

## What is collected — counters only

A closed, compile-time-enforced allow-list of thirteen counters. Each is a
`chart` with a small set of `buckets`; we keep weekly **counts** per
bucket and nothing else:

| Counter | Buckets |
|---|---|
| `dpm/install` | `linux` `darwin` `windows` — **once per machine** on the first non-CI run (a device-count proxy; no identifier) |
| `dpm/command` | the localnet verb (`up`, `down`, `dar`, `token`, …) |
| `dpm/command_exit` | `<verb>/ok` or `<verb>/fail` |
| `dpm/token_action` | the token subcommand (`create` `mint` `transfer` `burn` `balance` …) — CIP-0112 flow visibility |
| `dpm/ui_feature` | Web UI screen touched per session (`dar` `explorer` `metrics` `tokens` `skills` `backup` `instances`) |
| `dpm/channel` | `stable` `nightly` `dev` |
| `dpm/os` | `linux` `darwin` `windows` |
| `dpm/arch` | `amd64` `arm64` |
| `dpm/ci` | `true` `false` |
| `dpm/llm_agent` | `claude` `copilot` `cursor` `gemini` `none` |
| `dpm/docker_engine` | `docker` `colima` `orbstack` `podman` `other` |
| `dpm/compose_version_bucket` | `v2.20-` `v2.20-v2.27` `v2.28+` |
| `dpm/doctor_fail` | failing `doctor` check ids (only on `doctor` failure) |

A week's file is literally:

```json
{
  "schema_version": 1,
  "week": "2026-W22",
  "counters": {
    "dpm/command": {"up": 5, "down": 3},
    "dpm/os": {"darwin": 8}
  }
}
```

We learn *"this week saw 5 `up` invocations on darwin/arm64"* — and
nothing else.

## What is **never** collected

No machine id, install uuid, or hashed hardware id. No IP retention. And
by construction — the model is counters, not events — no:

- instance / project / compose names, party ids, contract ids
- DAR names/hashes, package/module names
- JWT audiences/issuers/fingerprints, ports, endpoints, file paths
- command arguments beyond the verb, error messages, stack traces
- timestamps finer than the ISO week, environment variables, hostnames

There is no per-invocation row to profile and no identifier to correlate.

## How it works

- Counters accumulate in memory during a run and merge into the current
  week's local file (`<config dir>/canton-devkit/telemetry/<week>.json`)
  on exit. Recording never blocks or fails a command.
- A **completed** past week is uploaded once (a single POST), then its
  file is deleted. On the first upload failure the week is marked deferred
  and retried at the next window; after a second miss it is dropped.
  Retrying an aggregate is privacy-safe; retrying individual events is
  not, so we don't keep events.
- With **no collector configured** (the default in a source build),
  nothing ever leaves the machine. Release binaries may bake an endpoint;
  `telemetry status` shows whether one is set.

## Audit it

```bash
canton-devkit telemetry status              # on/off, the rule that decided it, channel, collector
canton-devkit telemetry preview             # this period's counters (exactly what would be sent)
canton-devkit telemetry preview --format json
canton-devkit telemetry flush               # send all queued counters now (skip the daily window)
DPM_TELEMETRY_DEBUG=1 canton-devkit localnet status   # print the would-send JSON to stderr, send nothing
```

See also: [proposals/telemetry.md](proposals/telemetry.md) (full design) ·
[FAQ](faq.md) · [getting-started](getting-started.md).
