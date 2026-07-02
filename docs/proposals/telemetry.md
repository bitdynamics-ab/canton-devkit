# Telemetry — privacy-first usage counters

**Status:** Implemented (v1.0, ship-dark) · **Scope:** v1

> v1.0 ships the CLI-side: counter package, allow-list, opt-out notice,
> precedence + `DPM_TELEMETRY` + `DPM_TELEMETRY_DEBUG`, `App.Run` wiring,
> the root `telemetry` command, and the golden tests. No production
> collector is deployed yet — with no endpoint baked in, counters stay
> local. **Consent model: opt-out** — telemetry is **on by default** and
> users disable it anytime (`telemetry off` / `DPM_TELEMETRY=off` /
> `DO_NOT_TRACK=1`).

## Goal

Lightweight, anonymous usage telemetry that shows **what's used** and
**what breaks** without compromising the privacy posture (loopback-only
UI, JWT redaction, no PII in commits).

## Non-goals

Identifying users or machines (no hardware-derived id, no IP) · capturing
what a command ran against (no instance/party/contract ids, paths,
hostnames, ports) · error content · sessionizing/sequencing invocations ·
any flow enabling a behavioral profile. *One* exception, scoped tightly:
a single random, hardware-independent **install token** is sent solely to
de-duplicate install counts (Design #2) — it links to nothing else.

## Design

1. **Opt-out — telemetry is ON by default; users opt out anytime.**
   On the first operational command a one-time TTY-gated notice states
   it plainly: *"Telemetry is ON by default. Turn it off anytime:
   `canton-devkit telemetry off` (or `DPM_TELEMETRY=off` /
   `DO_NOT_TRACK=1`)."* All three switches disable it, and the choice
   persists. Non-interactive runs never prompt and never block.
2. **One anonymous install token, nothing else.** No machine id, no
   hashed hardware id, no IP retention. The single exception is a random
   UUIDv4 (`install_id`) minted client-side and stored in the telemetry
   config — *not* derived from any hardware attribute. It rides alongside
   counter uploads so the collector can count DISTINCT installs (the one
   adoption number additive counters can't yield), and is stored there
   ALONE as `(token, active-date)`, never joined to a counter. It is
   per-config-file (a fresh container/VM/reinstall mints a new one),
   suppressed in CI, and rotatable via `telemetry reset-id`. Counters
   themselves still merge into a daily aggregate with no per-invocation
   row.
3. **Counter taxonomy (14 slots).** Closed, compile-time-enforced
   allow-list (`internal/telemetry/allowlist.go`): `dpm/command`,
   `dpm/command_exit`, `dpm/channel`, `dpm/os`, `dpm/arch`, `dpm/ci`,
   `dpm/llm_agent`, `dpm/docker_engine`, `dpm/compose_version_bucket`,
   `dpm/doctor_fail`, `dpm/token_action`, `dpm/ui_feature`,
   `dpm/install`, `dpm/install_surface`. See
   [docs/telemetry.md](../telemetry.md) for buckets.
4. **Never collected.** instance/project/compose names · party/contract
   ids · JWT fields · DAR/package/module names · file paths · hostnames ·
   IP/MAC · args beyond the verb · error messages · stack traces · ports ·
   env names/values · sub-week timestamps.
5. **Transport.** No event queue. Counters live in a local weekly file.
   A completed past week uploads once (single POST, 2 s timeout, no
   inner retries); on failure → mark deferred, retry next window; after 2
   misses → drop. Retrying an aggregate is privacy-safe; events are not.
6. **Collector.** Custom minimal endpoint `POST /v1/counters` with body
   `{schema_version, period, granularity, counters, install_id?}` — not a
   SaaS events API. The optional `install_id` is recorded only in a
   separate `seen_install (token, active-date)` table for unique-install
   counts; it is never stored beside a counter.
7. **Retention.** Local file: **current week + 3 prior weeks** (rolling
   4-week window — useful for offline debug, still no per-event row, no
   sub-week timestamp, no id). Server raw intake: 24 h. Server aggregates:
   180 days. Dashboard: aggregated weeks only. (v1.1, server-side.)
8. **Small-cell suppression.** Start at **k = 3** for v1; ratchet upward
   (5 → 10) as the install base grows. Encoded as a config knob, not a
   structural change. (v1.1, server-side.)
9. **Disclosure UX.** TTY-gated one-time notice on the first *operational*
   localnet verb; never on `version`/help/`telemetry …`/non-TTY.
10. **Precedence.** `DO_NOT_TRACK` → `DPM_TELEMETRY` → config file →
    default on. `DPM_TELEMETRY_DEBUG=1` → print the would-send JSON to
    stderr, skip the network.
11. **CLI surface.** Root-level `canton-devkit telemetry on|off|status|preview`.
12. **Web UI parity.** Settings toggle + `/api/telemetry` GET/POST +
    `/api/telemetry/preview`, loopback-only. Optional — only build if a
    real user surface motivates it; the CLI surface plus
    `DPM_TELEMETRY_DEBUG=1` is the audit path operators actually need.
13. **Code shape.** `internal/telemetry/{allowlist,counter,store,config,
    uploader,notice,context}.go`.
14. **Hook point.** `internal/cli/app.go` `App.Run`, after Cobra's
    `root.ExecuteC()` returns. The verb derives from the executed
    `*cobra.Command` via the `localnetVerb(cmd)` helper (NOT `args[0]`,
    which would always be `"localnet"` for `canton-devkit localnet up`).
    The sink is installed via `App.WithTelemetry()`; tests leave the
    package's no-op sink so they never write or send.
15. **Channel detection.** `-ldflags -X main.channel=stable|nightly`;
    defaults to `dev` for local `go build`.
16. **Domain.** `telemetry.canton-devkit.dev` subdomain. (v1.1.)
17. **Tests — defense in depth.** Two-layer enforcement of the allow-list:
    (a) compile-time `go/ast` walk over every `telemetry.Inc(chart, bucket)`
    literal in the tree; (b) **runtime allow-list check inside `Inc()`**
    that silently drops unknown `(chart, bucket)` pairs — closes the gap
    where buckets are concatenated at runtime (e.g.
    `Inc("dpm/command_exit", verb+"/"+outcome)`) which the AST can't
    enumerate. Plus `DO_NOT_TRACK`/precedence tests, weekly-merge no-id
    /no-timestamp guard, 2-attempt-drop upload, `TestRunIsArgvOnly`
    still green.
18. **Public artifacts.** This doc; allow-list + sender code in the public
    repo; schema changes bump `schema_version` + update this doc.

## Pending (not in v1.0)

- **v1.1** — collector endpoint + nginx (no IP/UA/cookies) + weekly rollup
  + k = 3-anonymized public dashboard at `telemetry.canton-devkit.dev`.
- **v1.2** — Web UI parity (`/api/telemetry` + Settings panel), per the
  AGENTS.md CLI ↔ UI rule. Optional; only build if a real user surface
  motivates it.
- Bake a `nightly` channel build (`[nightly]` commit trigger) when
  nightly releases start.
