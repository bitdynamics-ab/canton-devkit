# Demo video recording guide

Two short videos, one per grant milestone:

- **Video A** — [Milestone 1: LocalNet Management — CLI](https://github.com/canton-foundation/canton-dev-fund/issues/386) (~9-11 min)
- **Video B** — [Milestone 2: Web UI, Observability, Monitoring, DAR & Contract Tooling, Optional AI Agent Skill Documents](https://github.com/canton-foundation/canton-dev-fund/issues/387) (~13-16 min)

**The two linked issues are the source of truth for what must be shown.**
Every deliverable bullet in each issue is quoted verbatim and traced to a
segment in the traceability tables below (§2 and §4) — use those tables to
confirm full coverage before publishing, and again to line up the final
video against the issue when submitting it to the committee.

Segments are ordered for a natural presenter flow, not the issue's bullet
order — the traceability tables are what guarantee nothing is missed, not
the running order. [`scripts/demo.sh`](../../scripts/demo.sh) is a
convenience script covering part of Milestone 1 plus the V2 token flow;
it's referenced below only where it's a handy source for exact,
already-tested command syntax — it is **not** what the video segments are
organized around.

> **Timing hazard:** `localnet up` is the long pole. Cached, it's ~90s; a
> true cold start (fresh image pull) can be 3-5 min, more on first run on
> Apple Silicon. **Never wait on camera.** Pre-warm before recording (see
> checklist) and hard-cut or speed-ramp over any live boot you do capture.

---

## 1 · Pre-flight checklist (do this before you hit record)

- [ ] `make build && make frontend` — release-equivalent binary with the real
      Web UI baked in (a binary built without `make frontend` serves a
      placeholder bundle — don't record that by accident).
- [ ] `dpm version` — sanity check the binary you're demoing.
- [ ] `dpm localnet doctor` — confirm the recording machine is
      healthy (Docker daemon, Compose v2, disk, memory, ports) *before* you're
      on camera.
- [ ] **Pre-warm the image cache**: run `dpm localnet up demo` once,
      let it fully finish, then `dpm localnet down demo`. The Splice archive and
      container images are now cached — the *next* `up` you record will be
      close to the ~90s floor instead of a cold multi-minute pull.
- [ ] Bring up **two** long-lived instances ahead of time so segments that
      need "already running" don't force you to wait live:
      - `demo` — default profile, used for most of Video A and the Web UI /
        DAR / contracts segments of Video B.
      - `demo-tokens` — `up demo-tokens --version token-standard-v2 --profile tokens-v2`,
        used only for the V2 token segment (`create`/`mint`/`burn` need the
        alpha channel; read/transfer/faucet work on stable).
- [ ] Terminal: large font (≥18pt), keep ANSI colors on (`NO_COLOR` unset),
      clean single-line prompt, ~100-column width, no other clutter in the
      title bar (instance names, ports, etc.).
- [ ] Screen recorder ready to capture both a terminal window and a browser
      window (Video B needs the Web UI + optionally a Grafana tab).
- [ ] Have `--port-base 31000` memorized for the two-instance segment so the
      second `up` doesn't collide with the pre-warmed `demo`.
- [ ] Have `docs/getting-started.md` (compatibility matrix) and
      `examples/ci/github-actions.yml` open in a tab/editor for the two
      "show the doc/file, don't just say it" beats in Video A/B.

---

## 2 · Traceability — Milestone 1 (issue #386)

Every deliverable bullet from the issue, quoted, mapped to where it's
demonstrated in Video A. "Live" = shown running on screen; "shown" = a doc
or file is put on screen; "verbal" = stated by the presenter, not a visual.

| Deliverable (quoted from #386) | Video A segment | How |
|---|---|---|
| `dpm localnet up/down/restart/clean/status/logs` CLI commands … with auto-generated configs, keys, identities, and printed endpoints and credentials | Up; Status/list/env/creds/logs | live |
| Version pinning (`--version`) and basic named-instance isolation (`--name`) using deterministic Docker Compose project names, labels, and explicit port configuration | Version pinning; Two-instance isolation | live |
| Snapshot and restore (`dpm localnet snapshot/restore`) | Snapshot & restore | live |
| Native DPM component packaging (`component.yaml` + OCI publish) — installable via `dpm install package` | Intro | shown (mention `component.yaml`, run `dpm install package canton-devkit` conceptually — no separate DPM registry needed for the demo, state it clearly) |
| Standalone Go binary release artifacts for macOS arm64, Linux amd64, Windows amd64, with checksums | Intro | shown (open the Releases page assets list) |
| Installation and "Getting Started" guide for both install paths on macOS/Linux/Windows | Intro | shown (`docs/getting-started.md`) |
| Docker preflight checks in `up` (Docker CLI, daemon, Compose v2, ports, disk, memory, host-specific prerequisites) | Doctor | live + verbal enumeration |
| `dpm localnet doctor` diagnostics (same checklist) | Doctor | live |
| Deterministic exit codes and readiness wait behavior suitable for headless automation | JSON + exit codes | live (`echo $?`) + verbal |
| Compatibility matrix (supported Splice version, macOS/Linux/Windows) | Version pinning | shown (`docs/getting-started.md` §4) |
| Demo script: startup, readiness, status, logs, teardown, one two-instance run with explicit non-conflicting ports | entire video | live |
| Internal + external testing: zero-to-LocalNet in under 10 minutes | Zero-to-LocalNet metric | verbal + shown (`scripts/validate-zero-to-localnet.sh`) |
| Adoption: ≥3 companies/teams reviewed and tested it | Close | verbal |

---

## 3 · Video A — Milestone 1: CLI lifecycle (~9-11 min)

### [00:00] Intro — the problem, the artifact
- Getting a full local Canton network running used to mean cloning Splice,
  decoding docker-compose layers, hunting JWT secrets, copy-pasting party IDs.
- dpm collapses that into a single binary, shipped two ways —
  a standalone Go binary (macOS arm64, Linux amd64, Windows amd64, checksums
  published per release) **and** a native DPM component
  (`dpm install package canton-devkit` → `dpm localnet up demo`, same
  binary either way).
- Briefly show `docs/getting-started.md` — install steps for all three OSes,
  both paths.

### [01:15] Doctor — host preflight
```
dpm localnet doctor
```
- This is the same preflight `up` runs automatically: Docker CLI present,
  daemon reachable, Compose v2 (not v1), required ports free, disk space,
  memory, and host-specific checks (Linux Docker-group permissions, Docker
  Desktop availability on macOS/Windows).
- Point out the per-check remediation hints it prints on a failure.

### [02:00] Up — one command
```
dpm localnet up demo
```
*(cut/speed-ramp over the boot — narrate over the pre-warmed run)*
- One command brings up Canton (participant + synchronizer), the Splice
  super-validator apps, three party wallets, Scan explorer, and signs the
  JWTs — a dozen containers, auto-generated configs/keys/identities.
- Point at the printed endpoints and credentials in the output.

### [03:00] Status, list, env, creds, logs
```
dpm localnet status demo
dpm localnet list
eval "$(dpm localnet env demo)"
dpm localnet creds demo
dpm localnet logs demo --tail 15
```
- `status` — health + ports at a glance.
- `list` — every instance this machine knows about.
- `env` — exports endpoints/creds directly into your shell for app or test
  config.
- `creds` — the signed JWTs directly, redacted by default.
- `logs` — tail any container without hunting for its name.

### [04:15] JSON output + deterministic exit codes → headless automation
```
dpm localnet status demo --format json
dpm localnet up bogus-name --port-base 1 ; echo "exit code: $?"
```
- Every inspection command has a stable `--format json` output with a
  `schema_version` — built for scripting, not just humans.
- Exit codes are deterministic and documented: `0` success, a specific
  non-zero code on a preflight failure (shown here), distinct from a
  generic error — safe to branch on in a CI script without scraping text.

### [05:15] Version pinning + compatibility matrix
```
dpm localnet versions
```
- Curated Splice version catalogue; call out the STATUS column — `supported`
  vs `drifted` (a security/currency signal) vs `available`.
- `up demo --version <tag>` pins a specific Splice release instead of
  `latest`.
- Show `docs/getting-started.md` §4 — the compatibility matrix (macOS
  arm64 / Linux amd64 / Windows amd64, all tested; Splice version list).

### [06:15] Two isolated instances, explicit ports
```
dpm localnet up demo-b --port-base 31000
dpm localnet list
```
- Each instance gets its own Docker Compose project name, network, and
  explicit non-conflicting port window — no manual bookkeeping.
- `list` now shows both, clearly separated.

### [07:30] Snapshot & restore
```
dpm localnet snapshot demo --to demo.tgz
dpm localnet clean --name demo
dpm localnet restore demo --from demo.tgz
```
- Snapshot pauses the node, dumps the database, and packages state into one
  tarball — application-consistent, not just crash-consistent.
- Tear the instance down completely (`clean`), then restore from the tarball
  and show it's back exactly where it was — hand that `.tgz` to a teammate or
  commit it as a CI fixture.

### [08:45] Zero-to-LocalNet in under 10 minutes
- Verbal + on-screen: this whole flow — install, `doctor`, `up`, `status` —
  is timed by `scripts/validate-zero-to-localnet.sh` against a 10-minute
  budget, run internally and validated by at least one external tester.
  (Don't re-run the timed harness live; reference it and, optionally, cut in
  a screen recording of a prior timed pass.)

### [09:15] Close
```
dpm localnet down demo
dpm localnet down demo-b
```
- Recap: one binary (standalone or DPM component), one command up,
  deterministic exit codes, snapshot/restore, named multi-instance
  isolation, JSON everywhere for automation.
- Mention: at least 3 companies/teams have reviewed and tested it for
  LocalNet setup and lifecycle.

---

## 4 · Traceability — Milestone 2 (issue #387)

| Deliverable (quoted from #387) | Video B segment | How |
|---|---|---|
| Web UI covering all CLI features from Milestone 1 with a user-friendly interface | UI walkthrough | live |
| Richer automation conveniences: machine-readable status output, environment export, named-instance discovery, enriched `doctor`, deeper troubleshooting | Automation conveniences | live |
| Example CI workflow: LocalNet startup, readiness wait, optional DAR upload, test execution, teardown | Example CI workflow | shown (`examples/ci/github-actions.yml`) |
| Bundled Prometheus/Grafana, per-component enable/disable, lightweight defaults, min-resources docs | Observability | live + shown (docs) |
| Canton-specific Grafana dashboard presets (tx/sec, completion latency, active contracts, per-template throughput) | Observability | live (Grafana tab) |
| `dpm localnet metrics` — dashboard URLs + text summary (throughput, latency p50/p99, resource usage) | Observability | live |
| DAR management CLI (upload/list/info/download/diff/remove/build-upload/watch), multi-participant, SCU-aware diff | DAR suite | live |
| DAR Web UI: drag-drop upload, per-participant vetting toggles, package explorer tree, diff viewer, hot-deploy indicator | DAR suite (UI half) | live |
| Contract tracking CLI (`contracts watch`, `tx ls/replay`) via Ledger API v2 | Contracts & transactions | live |
| Contract tracking Web UI "Explorer": live ACS table, transaction timeline, contract detail drawer, per-party visibility | Contracts & transactions (UI half) | live |
| Optional AI agent skill documents (lifecycle, DAR upload, package inspection, contract queries, log/status checks) | AI agent skills | live |
| Documentation: usage, dashboard customization, DAR workflows, contract explorer, AI skill docs | throughout | shown (docs referenced per segment) |
| Adoption: ≥5 companies/teams using it in daily workflow | Close | verbal |

---

## 5 · Video B — Milestone 2: Web UI, observability, DAR, contracts, tokens, skills (~13-16 min)

Assumes `demo` (default profile) and `demo-tokens` (`--profile tokens-v2`)
are already running from the pre-flight checklist.

### [00:00] Intro
- Everything in Video A has a CLI ↔ Web UI parity guarantee — same
  orchestration code behind both surfaces, no UI-only or CLI-only features.
- Today: the Web UI, richer automation conveniences, an example CI workflow,
  built-in observability, DAR tooling, live contract/tx inspection, and AI
  agent skill docs.

### [00:30] Web UI walkthrough
```
dpm localnet ui
```
- Loopback-only by default (security: refuses non-loopback bind without an
  explicit flag).
- Tour the top bar: instance switcher with live status dots, health pill,
  ⌘K command palette.
- Walk the pages: **Overview**, **Doctor**, **Wallet**, **Explorer**,
  **DAR Manager**, **Metrics**, **Tokens**, **Agent Skills** — call out that
  every one of these mirrors a CLI capability from Video A.

### [02:30] Automation conveniences
```
dpm localnet status demo --format json
dpm localnet env demo --format json
dpm localnet list --format json
dpm localnet doctor --format json
```
- Machine-readable status, `env` export for app/test config, named-instance
  discovery via `list`, and the enriched `doctor` output — all `--format
  json` with stable schemas, feeding both scripts and the Web UI itself.

### [03:30] Example CI workflow
- Show `examples/ci/github-actions.yml` (and mention the GitLab equivalent
  in the same folder) on screen: `up` → readiness wait → DAR upload → test
  execution → teardown.
- One line: this is the exact pattern from Video A's JSON/exit-code segment,
  packaged as a ready-to-copy workflow.

### [04:30] Observability — Prometheus & Grafana
```
dpm localnet observability enable demo --prometheus --grafana
dpm localnet metrics demo
```
- Sidecars toggle on a *running* instance, no restart needed; Prometheus and
  Grafana are independently enable/disable-able, with lightweight defaults
  and documented minimum resources when the full stack is on
  (`docs/observability.md`).
- `metrics` prints headline PromQL numbers — ledger TPS, mediator p50/p95/p99
  latency, JVM heap, DB connections — plus a clickable Grafana deep-link.
- Switch to the browser: open the Grafana link, show the bundled "Canton
  LocalNet — DApp Developer Overview" dashboard — call out the panels that
  match the issue directly: transactions/sec, command completion latency,
  active contract counts, per-template throughput.

### [07:00] DAR management
```
dpm localnet dar list demo
dpm localnet dar info <dar-path> --deep
dpm localnet dar upload <dar-path> --instance demo --role app-provider
dpm localnet dar diff <old.dar> <new.dar>
```
- `list --vetting` — which packages are vetted, per participant
  (multi-participant support).
- `info --deep` — parses Daml-LF, shows templates/choices/interfaces without
  touching a running node.
- `upload` — real `PackageService.UploadDar`, `--vet` by default,
  `--all-participants` to fan out.
- `diff` — SCU-aware upgrade signal severities (BLOCK/WARN/INFO) between two
  DAR versions.
- Switch to the browser DAR Manager page: drag-drop upload, package explorer
  tree, diff viewer, per-participant vetting toggles.
- Optional: `dar watch <path> --publish-to <ui-url>` and show the hot-deploy
  badge light up in the UI as you touch a source file.

### [10:00] Contracts & transactions
```
dpm localnet contracts ls demo
dpm localnet contracts watch demo
dpm localnet tx ls demo --limit 10
dpm localnet tx replay demo --id <updateId>
```
- `contracts ls` — active contract set snapshot at ledger end (Ledger API v2).
- `contracts watch` — live tail of create/archive events.
- `tx ls` — bounded recent-transaction list.
- `tx replay` — a single transaction rendered as an event tree, exercised
  choices and all.
- Mirror it in the browser: **Explorer** page — live ACS table, transaction
  timeline, contract detail drawer, explicit per-party visibility
  projection.

### [12:15] Token flows (bonus: CIP-0112 / V2, not a #387 line item but shares the ledger tooling)
Switch to the `demo-tokens` instance for this segment.
```
dpm localnet token demo --instance demo-tokens --symbol DEMO --supply 1000000 --decimals 6 --seed-holder
dpm localnet token balances --instance demo-tokens
```
- One command allocates an issuer, creates the instrument, mints, and funds
  a holder. Keep this brief — it's not an #387 deliverable, just a natural
  extension of the ledger tooling just shown.

### [13:15] AI agent skill documents
```
dpm localnet skills list
dpm localnet skills install --target claude
```
- Six bundled skill docs covering lifecycle, DAR upload, hot-deploy, contract
  inspection, token flows, and CI usage — safe, scoped workflows an AI coding
  agent can follow against a LocalNet.
- `skills install` drops them into `~/.claude/skills` (or `--target codex`).
- Show the **Agent Skills** page in the Web UI rendering the same docs.

### [14:45] Close
```
dpm localnet down demo
dpm localnet down demo-tokens
```
- Recap against the issue: Web UI at full CLI parity, richer automation
  conveniences, an example CI workflow, built-in observability with Canton
  dashboards, full DAR lifecycle, live contract/tx inspection, and AI agent
  skill docs.
- Mention: at least 5 companies/teams are using it in their daily Canton
  development workflow.

---

## 6 · Recording & editing notes

- **Hard-cut or speed-ramp** any live `up` boot, `dar watch` rebuild loop, or
  long-running `contracts watch` — don't make viewers wait in real time.
- **Lower-third callouts** worth adding in post:
  - The printed endpoints/JWTs right after `up` finishes.
  - The dev-JWT security warning: the fixed `unsafe` HMAC secret is for
    LocalNet only — **never reuse against MainNet or any non-LocalNet
    deployment**. Say this out loud at least once (in the `env`/`creds`
    segment) — don't let it be an easy-to-miss text overlay only.
  - The `versions` STATUS column meaning (`supported`/`drifted`).
  - The DAR `diff` severity glyphs (BLOCK/WARN/INFO).
- Keep terminal and browser windows large enough to read at 1080p/1440p
  export — zoom in during editing on small text (ports, JWTs, diff output)
  rather than trying to record already-zoomed.
- Confirm audio narration matches what's on screen for each bracketed
  timestamp — timestamps are targets, not hard cues; adjust pacing to what
  you actually record.

---

## 7 · Post-record coverage check

After editing, re-check every row of §2 (Milestone 1) and §4 (Milestone 2)
against the final cut — every deliverable needs at least a "shown" or
"verbal" mark. Link both final videos back on issues
[#386](https://github.com/canton-foundation/canton-dev-fund/issues/386) and
[#387](https://github.com/canton-foundation/canton-dev-fund/issues/387).
