# Demo video recording guide

Two short videos covering every deliverable in
[Milestone 1](https://github.com/canton-foundation/canton-dev-fund/issues/386)
(CLI LocalNet management) and
[Milestone 2](https://github.com/canton-foundation/canton-dev-fund/issues/387)
(Web UI, observability, DAR & contract tooling, AI agent skills) of the
canton-devkit grant.

- **Video A — Milestone 1: CLI lifecycle** — ~8-10 min
- **Video B — Milestone 2: Web UI, observability, DAR, contracts, tokens, skills** — ~12-15 min

Both scripts are built on the real CLI — every command below is copy-pasteable
and mirrors [`scripts/demo.sh`](../../scripts/demo.sh). Bullet points are
talking points, not word-for-word narration — say them in your own words.

> **Timing hazard:** `localnet up` is the long pole. Cached, it's ~90s; a true
> cold start (fresh image pull) can be 3-5 min, more on first run on Apple
> Silicon. **Never wait on camera.** Pre-warm before recording (see checklist)
> and hard-cut or speed-ramp over any live boot you do capture.

---

## 1 · Pre-flight checklist (do this before you hit record)

- [ ] `make build && make frontend` — release-equivalent binary with the real
      Web UI baked in (a binary built without `make frontend` serves a
      placeholder bundle — don't record that by accident).
- [ ] `canton-devkit version` — sanity check the binary you're demoing.
- [ ] `canton-devkit localnet doctor` — confirm the recording machine is
      healthy (Docker daemon, Compose v2, disk, memory, ports) *before* you're
      on camera.
- [ ] **Pre-warm the image cache**: run `canton-devkit localnet up demo` once,
      let it fully finish, then `localnet down demo`. The Splice archive and
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

---

## 2 · Coverage matrix

Every bullet from the two issues, mapped to where it's demoed. Use this after
recording to confirm nothing was skipped.

| Deliverable (issue) | Video / segment |
|---|---|
| `up/down/restart/clean/status/logs` CLI | A — Lifecycle tour |
| Auto-generated configs, keys, identities, printed endpoints/credentials | A — `up`, `env`, `creds` |
| `--version` pinning | A — Version pinning |
| `--name` / named-instance isolation, explicit ports | A — Two-instance segment |
| `snapshot` / `restore` | A — Snapshot & restore |
| DPM component packaging (`dpm install package`) | A — Intro/closing note |
| Standalone binary artifacts (macOS/Linux/Windows) | A — Intro note (mention, don't demo all 3 OSes) |
| Docker preflight checks in `up` | A — `doctor` |
| `doctor` diagnostics | A — `doctor` |
| Deterministic exit codes / readiness wait | A — mention during `up` and `doctor` |
| Demo script (startup/readiness/status/logs/teardown/two-instance) | A — entire video mirrors `scripts/demo.sh` |
| Web UI covering all CLI features | B — UI walkthrough |
| Machine-readable status (`--json`), `env` export, instance discovery | A — `--json` segment; B mentions UI parity |
| Enriched `doctor` | A — `doctor` |
| Example CI workflow | A — closing CI note |
| Prometheus/Grafana bundle, per-component enable/disable | B — Observability |
| Canton Grafana dashboard presets | B — Observability (Grafana tab) |
| `localnet metrics` | B — Observability |
| DAR management CLI (upload/list/info/download/diff/remove/build-upload/watch) | B — DAR segment |
| DAR Web UI (drag-drop, vetting, package tree, diff, hot-deploy) | B — DAR segment (UI half) |
| Contract tracking CLI (`contracts watch`, `tx ls/replay`) | B — Contracts/tx segment |
| Contract tracking Web UI "Explorer" | B — Explorer segment |
| AI agent skill documents | B — Skills segment |

---

## 3 · Video A — Milestone 1: CLI lifecycle (~8-10 min)

### [00:00] Intro — the problem
- Getting a full local Canton network running used to mean cloning Splice,
  decoding docker-compose layers, hunting JWT secrets, copy-pasting party IDs.
- canton-devkit collapses that into a single binary — same tool ships as a
  standalone Go binary (macOS arm64, Linux amd64, Windows amd64) **and** as a
  DPM component (`dpm install package canton-devkit`).
- Everything today is the real CLI, nothing scripted or faked.

### [00:45] Doctor — host preflight
```
canton-devkit localnet doctor
```
- Checks Docker CLI, daemon connectivity, Compose v2, ports, disk, memory —
  before you even try to start anything.
- Point out the remediation hints it prints on a failing check, and that `up`
  runs the same preflight automatically.

### [01:30] Up — one command
```
canton-devkit localnet up demo
```
*(cut/speed-ramp over the boot — narrate over the pre-warmed run)*
- One command brings up Canton (participant + synchronizer), the Splice
  super-validator apps, three party wallets, Scan explorer, and signs the
  JWTs — a dozen containers.
- Point at the printed endpoints and credentials in the output.

### [02:30] Status, list, env, logs
```
canton-devkit localnet status demo
canton-devkit localnet list
eval "$(canton-devkit localnet env demo)"
canton-devkit localnet logs demo --tail 15
```
- `status` — health + ports at a glance.
- `list` — every instance this machine knows about.
- `env` — exports endpoints/creds directly into your shell for app or test
  config.
- `logs` — tail any container without hunting for its name.

### [04:00] Version pinning
```
canton-devkit localnet versions
```
- Curated Splice version catalogue; call out the STATUS column — `supported`
  vs `drifted` vs `available` is a security/currency signal, not just a list.
- Mention `up demo --version <tag>` pins a specific Splice release instead of
  `latest`.

### [04:45] Two isolated instances, explicit ports
```
canton-devkit localnet up demo-b --port-base 31000
canton-devkit localnet list
```
- Each instance gets its own Docker Compose project, network, and port
  window — no manual port bookkeeping, no collisions.
- `list` now shows both, clearly separated.

### [06:00] Snapshot & restore
```
canton-devkit localnet snapshot demo --to demo.tgz
canton-devkit localnet clean --name demo
canton-devkit localnet restore demo --from demo.tgz
```
- Snapshot pauses the node, dumps the database, and packages state into one
  tarball — application-consistent, not just crash-consistent.
- Tear the instance down completely (`clean`), then restore from the tarball
  and show it's back exactly where it was — hand that `.tgz` to a teammate or
  commit it as a CI fixture.

### [07:15] JSON output + CI story
```
canton-devkit localnet status demo --format json
canton-devkit localnet up ci --json > /tmp/instance.json
```
- Every inspection command has a stable `--format json` / `--json` output
  with a `schema_version` — built for scripting and CI, not just humans.
- One-line mention: same pattern (`up` → tests → `clean`) is the whole example
  CI workflow in the docs.

### [08:00] Close
```
canton-devkit localnet down demo
canton-devkit localnet down demo-b
```
- Recap: one binary, one command up, deterministic exit codes, snapshot/restore,
  named multi-instance isolation, JSON everywhere for automation.

---

## 4 · Video B — Milestone 2: Web UI, observability, DAR, contracts, tokens, skills (~12-15 min)

Assumes `demo` (default profile) and `demo-tokens` (`--profile tokens-v2`)
are already running from the pre-flight checklist.

### [00:00] Intro
- Everything in Video A has a CLI ↔ Web UI parity guarantee — same
  orchestration code behind both surfaces, no UI-only or CLI-only features.
- Today: the Web UI, built-in observability, DAR tooling, live contract/tx
  inspection, token flows, and AI agent skill docs.

### [00:30] Launch the Web UI
```
canton-devkit localnet ui
```
- Loopback-only by default (security: refuses non-loopback bind without an
  explicit flag).
- Tour the top bar: instance switcher with live status dots, health pill,
  ⌘K command palette for fuzzy-jumping between instances and pages.
- Walk the pages: **Overview**, **Doctor**, **Wallet**, **Explorer**,
  **DAR Manager**, **Metrics**, **Tokens**, **Agent Skills**.

### [02:30] Observability — Prometheus & Grafana
```
canton-devkit localnet observability enable demo --prometheus --grafana
canton-devkit localnet metrics demo
```
- Sidecars toggle on a *running* instance, no restart needed; per-component
  (Prometheus/Grafana independently).
- `metrics` prints headline PromQL numbers — ledger TPS, mediator p50/p95/p99
  latency, JVM heap, DB connections — plus a clickable Grafana deep-link.
- Switch to the browser: open the Grafana link, show the bundled "Canton
  LocalNet — DApp Developer Overview" dashboard (10 panels, `$instance`
  template var) — call out throughput, latency, active contracts, per-template
  panels.

### [05:00] DAR management
```
canton-devkit localnet dar list demo
canton-devkit localnet dar info <dar-path> --deep
canton-devkit localnet dar upload <dar-path> --instance demo --role app-provider
canton-devkit localnet dar diff <old.dar> <new.dar>
```
- `list --vetting` — which packages are vetted, per participant.
- `info --deep` — parses Daml-LF, shows templates/choices/interfaces without
  touching a running node.
- `upload` — real `PackageService.UploadDar`, `--vet` by default,
  `--all-participants` to fan out.
- `diff` — SCU-aware upgrade signal severities (BLOCK/WARN/INFO) between two
  DAR versions — a real safety net before you ship a schema change.
- Switch to the browser DAR Manager page: drag-drop upload, package explorer
  tree, diff viewer, per-participant vetting toggles.
- Optional: `dar watch <path> --publish-to <ui-url>` and show the hot-deploy
  badge light up in the UI as you touch a source file.

### [08:30] Contracts & transactions
```
canton-devkit localnet contracts ls demo
canton-devkit localnet contracts watch demo
canton-devkit localnet tx ls demo --limit 10
canton-devkit localnet tx replay demo --id <updateId>
```
- `contracts ls` — active contract set snapshot at ledger end.
- `contracts watch` — live tail of create/archive events.
- `tx ls` — bounded recent-transaction list.
- `tx replay` — a single transaction rendered as an event tree, exercised
  choices and all.
- Mirror it in the browser: **Explorer** page — live ACS table, transaction
  timeline, contract detail drawer, per-party visibility projection.

### [11:00] Token flows (CIP-0112 / V2)
Switch to the `demo-tokens` instance for this segment.
```
canton-devkit localnet token demo --instance demo-tokens --symbol DEMO --supply 1000000 --decimals 6 --seed-holder
canton-devkit localnet token balances --instance demo-tokens
canton-devkit localnet token transfer --instance demo-tokens --instrument DEMO --from <holder> --to <issuer> --amount 250 --auto-accept
```
- One command (`token demo`) allocates an issuer, creates the instrument,
  mints, and funds a holder — a full native V2 token live in seconds.
- `balances` — party × instrument matrix.
- `transfer --auto-accept` — the two-step V2 transfer/accept flow collapsed
  into one call for the demo.
- Switch to the browser **Tokens** page and show the same actions as buttons.

### [13:00] AI agent skill documents
```
canton-devkit localnet skills list
canton-devkit localnet skills install --target claude
```
- Six bundled skill docs covering lifecycle, DAR upload, hot-deploy, contract
  inspection, token flows, and CI usage — safe, scoped workflows an AI coding
  agent can follow against a LocalNet.
- `skills install` drops them into `~/.claude/skills` (or `--target codex`).
- Show the **Agent Skills** page in the Web UI rendering the same docs.

### [14:30] Close
```
canton-devkit localnet down demo
canton-devkit localnet down demo-tokens
```
- Recap: Web UI at parity with the CLI, built-in observability, full DAR
  lifecycle, live contract/tx inspection, native token flows, and AI agent
  skill docs — one toolkit end to end.

---

## 5 · Recording & editing notes

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

## 6 · Post-record coverage check

After editing, re-read [§2 Coverage matrix](#2--coverage-matrix) and confirm
every row has a corresponding clip in the final cut before publishing. Link
both final videos back on issues
[#386](https://github.com/canton-foundation/canton-dev-fund/issues/386) and
[#387](https://github.com/canton-foundation/canton-dev-fund/issues/387).
