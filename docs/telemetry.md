# Telemetry

canton-devkit records **anonymous, aggregate usage counters** — merged
into a daily total with **no per-invocation rows** — to help maintainers
see what's used and what breaks. The only identifier sent is a single
**anonymous random install token** (a UUID, not derived from any hardware
detail) used purely to count *distinct* installs; it never tags an
individual counter. This page is the complete reference for what is
collected, what is never collected, and how to inspect or disable it.

Inspect exactly what's queued any time:

```bash
canton-devkit telemetry preview
```

## On by default (opt-out)

Telemetry is **on by default**. The first time you run an operational
command in an interactive terminal, a one-time notice explains this. The
Debian package also records a one-time `apt` install-surface ping during
package installation; because that path is non-interactive, opt out
**before** install with `DPM_TELEMETRY=off` or `DO_NOT_TRACK=1` if you do
not want it. Turn telemetry off any time — your choice persists:

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

A closed, compile-time-enforced allow-list of fourteen counters. Each is a
`chart` with a small set of `buckets`; we keep daily **counts** per
bucket and nothing else:

| Counter | Buckets |
|---|---|
| `dpm/install` | `linux` `darwin` `windows` — **once per machine** on the first non-CI run (a device-count proxy; no identifier) |
| `dpm/install_surface` | `apt` — **once per machine** when Debian/Ubuntu package installation finishes |
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

A period file is literally:

```json
{
  "schema_version": 2,
  "period": "2026-06-21",
  "granularity": "daily",
  "counters": {
    "dpm/command": {"up": 5, "down": 3},
    "dpm/os": {"darwin": 8}
  }
}
```

We learn *"this day saw 5 `up` invocations on darwin/arm64"* — and
nothing else.

## The anonymous install token

One value is sent that *can* distinguish installs: a random **UUIDv4**
minted on first upload and stored in your telemetry config. It exists for
exactly one reason — so the collector can answer *"how many distinct
installs?"* (the one number pure counters can't give). What it is
**not**:

- **Not derived from your machine** — no hostname, MAC, serial, or
  hardware fingerprint feeds it. It's pure random bytes.
- **Not linked to your usage** — the collector stores it alone, as
  `(token, active-date)`, never beside a counter. We can count installs;
  we can't see what any one install did.
- **Per-environment, not per-person** — it lives in the config file, so a
  fresh container, VM, or reinstall mints a new one by design. It counts
  *environments*, not people.
- **Suppressed in CI** and **rotatable anytime** with
  `canton-devkit telemetry reset-id` (or cleared entirely when you
  `telemetry off`).

## What is **never** collected

No machine id, no hashed hardware id, no IP retention. And by
construction — the model is counters, not events — no:

- instance / project / compose names, party ids, contract ids
- DAR names/hashes, package/module names
- JWT audiences/issuers/fingerprints, ports, endpoints, file paths
- command arguments beyond the verb, error messages, stack traces
- timestamps finer than the ISO week, environment variables, hostnames

There is no per-invocation row to profile, and the one token we send
correlates only to itself (an install count) — never to your usage.

## How it works

- Counters accumulate in memory during a run and merge into the current
  day's local file (`<config dir>/canton-devkit/telemetry/<period>.json`)
  on exit. Recording never blocks or fails a command.
- The Debian package install hook uses the same spool/uploader path: it
  records the install-surface counter locally first, then does a
  best-effort flush so the install path is visible even if the user never
  runs an operational command later.
- A **completed** past period is uploaded once (a single POST), then its
  file is deleted. On the first upload failure the period is marked deferred
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

See also: [FAQ](faq.md) · [Getting started](getting-started.md).
