# Canton DevKit — Adoption & Usage Metrics

*A reviewer's guide: what we measure, what each number means, and how we
keep it private.*

---

## 1. What this is, in one paragraph

Canton DevKit reports a small set of **anonymous, aggregate usage
counters** so we (and the Committee) can see how the tool is actually
being used and adopted — which commands run, on which platforms, how the
CIP-0112 token flow is exercised, and roughly how many machines have
installed it. The data is **opt-out, zero-PII**, and flows into a
**self-hosted dashboard** the maintainer owns. No single number is
treated as proof of adoption; the picture is a **composite** of three
independent signals (see §3).

---

## 2. Privacy model — read this first

This is the foundation everything else rests on. Reviewers should be able
to trust the numbers *because* of how little is collected.

- **Opt-out, on by default.** A first-run notice tells users it's on and
  how to turn it off: `canton-devkit telemetry off`, or set
  `DPM_TELEMETRY=off` / `DO_NOT_TRACK=1`.
- **No identifier of any kind.** No user ID, machine ID, install UUID, IP,
  hostname, or account. The collector literally cannot tell one device
  from another.
- **Counters only — no events, no timestamps finer than the period.** We
  keep "command `up` ran 12 times this day," never "user X ran `up` at
  14:03." There are no per-invocation rows.
- **Closed allow-list, enforced at compile time.** Only the ~13 counters
  in §4 can ever be recorded; a test walks every recording call-site and
  fails the build if a new counter or bucket slips in.
- **No free-form text.** Buckets are derived from the command path and
  the OS — never from arguments, flags, file paths, instrument names, or
  party ids.
- **You can see and delete your own data anytime.** `telemetry preview`
  prints exactly what would be sent; the backend is your own Postgres, so
  `pg_dump` / CSV export / `TRUNCATE` are all in your hands.

What a single upload looks like (the **entire** payload):

```json
{ "schema_version": 2, "period": "2026-06-04", "granularity": "daily",
  "counters": { "dpm/command": {"up": 12, "down": 3}, "dpm/os": {"darwin": 9} } }
```

---

## 3. Three independent signals (the composite)

Adoption is judged on the **combination** of these — not any one alone.

| Signal | Source | Answers | Can it identify anyone? |
|---|---|---|---|
| **Telemetry** | the CLI, opt-out | *how the tool is used* + a device-count proxy | **No** |
| **GitHub** | release & repo API | *how many installs / how visible* | No (public) |
| **Qualitative** | maintainer-logged | *which named teams, with proof* | Names are intentional, public-facing |

Why three: telemetry is privacy-preserving and therefore *can't* count
unique installs precisely; GitHub download counts give an install proxy
telemetry can't; qualitative evidence names the actual teams. Together
they corroborate each other.

---

## 4. Every telemetry counter, explained

Each counter is a **chart** (the thing being measured) with a small set of
**buckets** (the allowed values). We store counts per bucket per period.

| Counter | Buckets | What it answers | Notes / caveats |
|---|---|---|---|
| **`dpm/install`** | `linux` `darwin` `windows` | *How many distinct machines have installed it?* (device-count proxy) | Increments **once per machine**, on the first non-CI run. See §5 + §6 for the important caveats. |
| **`dpm/command`** | the localnet verb (`up` `down` `status` `list` `dar` `token` …) | *Which features get used, and how much?* | Counts every localnet subcommand invocation. |
| **`dpm/command_exit`** | `<verb>/ok` or `<verb>/fail` | *How reliable is each command?* (e.g. did `up` succeed?) | Surfaces friction — a high `up/fail` rate flags a setup problem. |
| **`dpm/token_action`** | `create` `mint` `transfer` `burn` `balance` `…` | *Is the CIP-0112 token flow being exercised?* (M3) | The create → mint → transfer flow that M3 is graded on. |
| **`dpm/ui_feature`** | `dar` `explorer` `metrics` `tokens` `skills` `backup` `instances` | *Which Web UI screens do people actually use?* (M2) | Recorded **once per `localnet ui` session** so polling doesn't inflate it. |
| **`dpm/os`** | `linux` `darwin` `windows` | *What platforms is it run on?* (incl. the M1 Windows bar) | Counts **invocations**, not machines — see §5. |
| **`dpm/arch`** | `amd64` `arm64` | *What CPU architectures?* | Per invocation. |
| **`dpm/ci`** | `true` `false` | *How much usage is automated (CI) vs interactive?* | Headless-automation adoption (an M2 goal). |
| **`dpm/llm_agent`** | `claude` `copilot` `cursor` `gemini` `none` | *Is it being driven by an AI coding agent?* | Detected from well-known env vars; `none` when run by a human directly. |
| **`dpm/docker_engine`** | `docker` `colima` `orbstack` `podman` `other` | *What container runtimes are in the field?* | Helps prioritize compatibility. |
| **`dpm/compose_version_bucket`** | `v2.20-` `v2.20-v2.27` `v2.28+` | *What Docker Compose versions?* | Coarse buckets, never an exact version string. |
| **`dpm/doctor_fail`** | failing `doctor` check ids | *What pre-flight checks fail most?* (only on a `doctor` failure) | Tells us the most common environment blockers to fix. |

---

## 5. The two questions reviewers ask — and how we answer them

**Q1. "How *much* is it being used?"**
→ The usage counters: `dpm/command`, `dpm/command_exit`, `dpm/token_action`,
`dpm/ui_feature`, plus the context counters (`os`, `arch`, `ci`,
`llm_agent`). These are **invocation-weighted** — they tell you volume and
feature mix.

**Q2. "How *many devices / teams* are using it?"**
This is the subtle one. **Invocation counts can't answer it** — `dpm/os
darwin = 10` could be *10 machines × 1 command* **or** *1 machine × 10
commands*; with no identifier, the collector can't tell. So device/team
counts come from a **dedicated proxy plus the other two signals**:

- **`dpm/install`** — the privacy-safe device-count proxy. Each machine
  contributes exactly **one** increment, ever (the first non-CI run).
  `sum(dpm/install)` ≈ distinct installs, split by platform.
- **GitHub release downloads** — an independent install proxy.
- **`adoption_evidence`** — the named external teams, with links to their
  issues / demos / case studies.

> **Worked example.** If a dashboard shows `dpm/install = 7` but `dpm/os
> darwin = 240`, that's **7 machines** that together ran ~240 commands —
> not 240 users. The two numbers measure different things on purpose.

---

## 6. Honest caveats (so nobody over-reads a number)

We'd rather state these plainly than have a reviewer discover them.

- **`dpm/os` (and `arch`, `ci`, `llm_agent`) count *invocations*, not
  devices.** They answer "how much / on what," not "how many machines."
- **`dpm/install` counts *total installs*, not *active devices*.** A
  machine that installed once and never returned still counts once — it's
  like "downloads," not "monthly active users." Measuring *active* devices
  would need a per-device identifier, which we deliberately don't have.
- **`dpm/install` can't dedupe re-installs.** With no persistent
  identifier (the very thing that keeps it zero-PII), wiping the local
  config or using a fresh machine/container counts as a **new** install.
  So it's a **directional over-estimate proxy**, corroborated by GitHub
  downloads — not an exact unique-device count.
- **`dpm/install` is gated to non-CI on purpose.** CI runners are
  ephemeral (a fresh image per job), so counting first-runs there would
  make every CI job look like a brand-new install and inflate the number.
  CI usage is still captured truthfully by `dpm/ci`.
- **GitHub download counts are GitHub-side**, pulled from the public API —
  they are not something the CLI reports. (They also include re-downloads.)
- **`dpm/llm_agent` reflects the running environment.** Internal testing
  driven by an AI agent shows up as that agent, not as external users —
  read it as "context," not "N AI users."

---

## 7. How adoption maps to the milestones

Each milestone has an acceptance bar; here's which metric evidences it.

| Milestone | Acceptance bar (short) | Primary evidence |
|---|---|---|
| **M1** — LocalNet lifecycle, incl. Windows | 3 external teams run `up/status/down` cross-platform, incl. ≥1 Windows | `dpm/command` + `dpm/command_exit` (lifecycle + success), `dpm/os` (incl. `windows`), `dpm/install` · GitHub downloads · evidence |
| **M2** — Web UI, observability, DAR, explorer | 5 teams use the Web UI / DAR / explorer / observability against their own app | `dpm/ui_feature`, `dpm/ci`, `dpm/llm_agent` · evidence |
| **M3** — Token Standard (CIP-0112) | 7 projects demo create → mint → transfer (or mint → transfer → burn) | `dpm/token_action` · evidence |
| **M4** — composite adoption | ≥5 apps in real use, ≥250 cumulative installs, ≥2 workshops, 1 case study | **`dpm/install`** + **GitHub downloads** (the 250 floor) + **`adoption_evidence`** + stars/forks (visibility) |

---

## 8. How the data flows

```
  Each developer machine / VM            Your single, self-hosted backend
  ───────────────────────────            ────────────────────────────────
   canton-devkit CLI  ──HTTPS POST──▶   collector  ──▶  Postgres  ──▶  Metabase
   (opt-out counters)                   (sums across machines)         (dashboard)

   GitHub release/repo API  ──daily snapshot──▶  Postgres  (downloads, stars/forks)
   Maintainer (named teams) ──manual entry────▶  Postgres  (adoption_evidence)
```

- The collector **sums** submissions across machines (many machines
  reporting the same day aggregate, since there's no id to dedupe by).
- Postgres is **yours** — full export anytime (`pg_dump`, CSV, SQL).
- Metabase is open-source (self-hosted); every chart exports to CSV/Excel.

---

## 9. How to read the dashboard

The dashboard is grouped by milestone (M1 → M4), and every chart is
**tagged by source** so it's clear where the number comes from:

- **telemetry** — privacy-preserving CLI/Web-UI counters
- **GitHub** — release & repo signals
- **evidence** — maintainer-logged external teams

So a reviewer can scan top-to-bottom: M1 lifecycle & platforms → M2 Web-UI
& automation → M3 token flow → M4 installs / downloads / named teams.

---

## 10. What we are *not* claiming

- We are **not** tracking individuals or sessions.
- We are **not** claiming `dpm/install` is an exact unique-device count —
  it's a corroborated proxy.
- We are **not** inferring identity by combining counters — they're all
  coarse aggregates with no join key.

The goal is an **honest, privacy-preserving, multi-signal** view of
adoption that a reviewer can trust precisely because of what it refuses to
collect.

---

*Related: [`docs/telemetry.md`](telemetry.md) (the full counter spec and
opt-out details) and [`telemetry-collector/`](../telemetry-collector/)
(the self-hosted backend + dashboard provisioning).*
