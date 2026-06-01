# Telemetry — privacy-first usage counters

**Status:** Implemented (v1.0, ship-dark) · **Scope:** v1

> v1.0 ships the CLI-side: counter package, allow-list, opt-out notice,
> precedence + `DPM_TELEMETRY` + `DPM_TELEMETRY_DEBUG`, `App.Run` wiring,
> the root `telemetry` command, and the golden tests. No production
> collector is deployed yet — with no endpoint baked in, counters stay
> local. **One deliberate change from the original draft:** consent is
> **opt-out** (on by default), not opt-in, per project decision; every
> other guardrail below is unchanged.

## Goal

Lightweight, anonymous usage telemetry that shows **what's used** and
**what breaks** without compromising the privacy posture (loopback-only
UI, JWT redaction, no PII in commits).

## Non-goals

Identifying users/machines/installs (no IDs of any kind) · capturing what
a command ran against (no instance/party/contract ids, paths, hostnames,
ports) · error content · sessionizing/sequencing invocations · any flow
enabling a behavioral profile.

## Decisions

1. **Opt-out, default on.** (Changed from the draft's opt-in.) One-time
   TTY-gated notice on the first operational command; `telemetry off` /
   `DPM_TELEMETRY=off` / `DO_NOT_TRACK=1` disable it.
2. **No identifier at all.** No machine id, install uuid, hashed hardware
   id, or IP retention. Counters merge into a weekly aggregate with no
   per-invocation row.
3. **Counter taxonomy (10 slots).** Closed, compile-time-enforced
   allow-list (`internal/telemetry/allowlist.go`): `dpm/command`,
   `dpm/command_exit`, `dpm/channel`, `dpm/os`, `dpm/arch`, `dpm/ci`,
   `dpm/llm_agent`, `dpm/docker_engine`, `dpm/compose_version_bucket`,
   `dpm/doctor_fail`. See [docs/telemetry.md](../telemetry.md) for buckets.
4. **Never collected.** instance/project/compose names · party/contract
   ids · JWT fields · DAR/package/module names · file paths · hostnames ·
   IP/MAC · args beyond the verb · error messages · stack traces · ports ·
   env names/values · sub-week timestamps.
5. **Transport.** No event queue. Counters live in a local weekly file.
   A completed past week uploads once (single POST, 2 s timeout, no
   inner retries); on failure → mark deferred, retry next window; after 2
   misses → drop. Retrying an aggregate is privacy-safe; events are not.
6. **Collector.** Custom minimal endpoint `POST /v1/counters` with body
   `{schema_version, week, counters}` — not a SaaS events API. (v1.1, not
   yet deployed.)
7. **Retention.** Local file: current week. Server raw: 24 h. Aggregates:
   180 days. Dashboard: aggregated weeks only. (v1.1, server-side.)
8. **Small-cell suppression.** k = 10 at rollup time. (v1.1, server-side.)
9. **Disclosure UX.** TTY-gated one-time notice on the first *operational*
   localnet verb; never on `version`/help/`telemetry …`/non-TTY.
10. **Precedence.** `DO_NOT_TRACK` → `DPM_TELEMETRY` → config file →
    default on. `DPM_TELEMETRY_DEBUG=1` → print the would-send JSON to
    stderr, skip the network.
11. **CLI surface.** Root-level `canton-devkit telemetry on|off|status|preview`.
12. **Web UI parity.** Settings toggle + `/api/telemetry` GET/POST +
    `/api/telemetry/preview`, loopback-only. (v1.2 — pending; see below.)
13. **Code shape.** `internal/telemetry/{allowlist,counter,store,config,
    uploader,notice,context}.go`.
14. **Hook point.** `internal/cli/app.go` `App.Run`, after exit-code
    normalization — the single chokepoint seeing the verb + final code.
    The sink is installed via `App.WithTelemetry()`; tests leave the
    package no-op sink so they never write or send.
15. **Channel detection.** `-ldflags -X main.channel=stable|nightly`;
    defaults to `dev` for local `go build`.
16. **Domain.** `telemetry.canton-devkit.dev` subdomain. (v1.1.)
17. **Tests.** Compile-time allow-list walk (go/ast over every
    `telemetry.Inc` call site), `DO_NOT_TRACK`/precedence, weekly merge +
    no-id/no-timestamp guard, 2-attempt-drop upload, `TestRunIsArgvOnly`
    still green.
18. **Public artifacts.** This doc; allow-list + sender code in the public
    repo; schema changes bump `schema_version` + update this doc.

## Pending (not in v1.0)

- **v1.1** — collector endpoint + nginx (no IP/UA/cookies) + weekly rollup
  + k-anonymized public dashboard at `telemetry.canton-devkit.dev`.
- **v1.2** — Web UI parity (`/api/telemetry` + Settings panel), per the
  AGENTS.md CLI ↔ UI rule.
- Bake a `nightly` channel build (`[nightly]` commit trigger) when nightly
  releases start.

## Open questions

None — channel, domain, banner timing, and the opt-out decision are locked.
