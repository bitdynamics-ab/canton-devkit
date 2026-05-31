# Telemetry — privacy-first usage counters

**Status:** Design (pre-implementation)
**Owner:** TBD
**Scope:** v1

## Goal

Ship lightweight, anonymous usage telemetry that lets the team see
**what's used** and **what breaks** — without compromising the
project's existing privacy posture (loopback-only UI, JWT redaction,
no PII in commits, stripped query strings in access logs).

## Non-goals

- Identifying users, machines, or installs (no IDs of any kind)
- Capturing what a command was run *against* (no instance names,
  party IDs, contract IDs, file paths, hostnames, ports)
- Capturing error content (no stack traces, no error messages)
- Sessionizing or sequencing invocations
- Any data flow that could enable a behavioral profile after the
  fact

## Decisions (locked)

### 1. Opt-in, default off

| | |
|---|---|
| Default | `off` |
| Mode | opt-in |
| Rationale | Project culture is privacy-first at every other fork (loopback bind, JWT redaction, no PII in commits). Opt-out would stick out. Go's `gotelemetry` precedent. |

### 2. No identifier at all

No machine ID, no install UUID, no hashed hardware ID, no IP retention.
Counters are merged into a weekly aggregate with no per-invocation row.
We learn *"this week saw N `up` invocations from arm64/darwin"* — and
nothing else.

This kills the GitHub-CLI-2.91 device-id behavioral-profile critique
outright, and removes any legal/GDPR exposure because no personal
data is ever collected.

### 3. Counter taxonomy (v1, ten slots)

Closed allow-list, compile-time enforced. `<chart>:<bucket>` format.
No timestamps below the week, no IDs, no free text.

| # | Counter | Allowed buckets |
|---|---|---|
| 1 | `dpm/command` | `up` `down` `restart` `status` `list` `doctor` `logs` `env` `creds` `clean` `snapshot` `restore` `versions` `ui` `container` `refresh` `metrics` `contracts` `tx` `dar` `skills` `telemetry` |
| 2 | `dpm/command_exit` | `<verb>/<ok\|fail>` — outcome derived from `errors.As(err, &localnet.ExitCodeError)` in `App.Run` |
| 3 | `dpm/channel` | `stable` `nightly` `dev` |
| 4 | `dpm/os` | `linux` `darwin` `windows` |
| 5 | `dpm/arch` | `amd64` `arm64` |
| 6 | `dpm/ci` | `true` `false` |
| 7 | `dpm/llm_agent` | `claude` `copilot` `cursor` `gemini` `none` (unknown agents bucket to `none`, **not** `other`) |
| 8 | `dpm/docker_engine` | `docker` `colima` `orbstack` `podman` `other` |
| 9 | `dpm/compose_version_bucket` | `v2.20-` `v2.20-v2.27` `v2.28+` (matches the `preflight` buckets) |
| 10 | `dpm/doctor_fail` | check IDs from `internal/docker/checks.go` allow-list (only emitted on `doctor` exit ≠ 0) |

Dropped from earlier drafts: `dpm/ui_session`, `dpm/feature_used`, and
`dpm/version`. The first two are second-order; the third was replaced
by `dpm/channel` because semver is more fingerprinting.

### 4. What we **never** collect (canton-devkit landmines)

Instance / project / compose names · party IDs · contract IDs · JWT
audiences / issuers / signing key fingerprints · Daml package names ·
DAR hashes · module names · file paths · working directories · git
remotes · hostnames · IP addresses · MAC addresses · command arguments
beyond the verb · error messages · stack traces · port numbers ·
environment variable names or values.

Every dimension above is either crypto-derived but recognizable
(party IDs, package hashes), customer-named in the wild (instance
names = "$customer-poc-net"), or directly secret (JWTs).

### 5. Transport

- Async, fire-and-forget HTTPS POST
- **No event queue.** Counters live in a local weekly file; nothing
  goes over the wire at invocation time.
- Weekly upload window tries once. On failure, mark "deferred" and
  try once more at next week's window. After 2 misses, drop the
  file. Retrying the same aggregate is privacy-safe — retrying
  individual *events* is not, so we don't.
- 2 s hard timeout, no retries inside the attempt
- Body ≤ a few KB, one POST per week per machine

### 6. Collector

Custom minimal endpoint, not direct ingestion to an analytics SaaS.

```
POST https://telemetry.canton-devkit.dev/v1/counters
Content-Type: application/json

{
  "schema_version": 1,
  "week": "2026-W22",
  "counters": {
    "dpm/command": {"up": 5, "down": 3},
    "dpm/channel": {"stable": 1},
    ...
  }
}
```

Why not Plausible / PostHog Events API directly: their endpoints expect
`domain` / `url` / visitor-counting headers (`User-Agent`, `X-Forwarded-For`)
and are oriented around page-view sessionization. We don't want any of
that surface.

Ingress:
- nginx in front with `access_log off`, no IP/UA forwarded
- Real-IP stripped at ingress (`set_real_ip_from` deliberately omitted)
- Body-only ingestion, no query string, no cookies

### 7. Retention

| Stage | Retention | Notes |
|---|---|---|
| Local counter file | current week only | Rotated on successful or twice-attempted upload |
| Server raw intake | **24 h** | Long enough to recover from a bad rollup |
| Server aggregates | **180 days** | Half of Homebrew's 365 d — same product question, shorter blast radius |
| Public dashboard | aggregated weeks only | Never raw |

### 8. Small-cell suppression on public dashboard

Threshold **k = 10**: buckets with weekly count < 10 render as `<10`
and are excluded from sorted ranks. Implemented at **rollup time**, not
at query time, so the published dataset itself never exposes small
cells. Defends against bucket-uniqueness fingerprinting on rare combos
(e.g. macOS / arm64 / Colima).

### 9. Disclosure UX

**When to prompt:** on first interactive invocation of an *operational*
`localnet` subcommand (e.g. `up`, `down`, `status`, `doctor`). Not on:

- `canton-devkit version`
- `canton-devkit --help` / root help / any `-h` flag
- `canton-devkit telemetry status | preview` (users must be able to
  inspect before deciding)
- non-TTY runs (CI, scripts, piped stdout)

**Banner text** (one-shot, persisted to config file):

```
canton-devkit can send anonymous usage counters (command name, OS,
Docker engine, exit status) to help us prioritize fixes. No IDs, no
file paths, no party IDs, no JWTs, no error messages — just counters,
aggregated weekly. Source: github.com/bitdynamics-ab/canton-devkit
Public data: telemetry.canton-devkit.dev

Telemetry is OFF by default. Turn it on?  [y/N]
You can change this anytime with:  canton-devkit telemetry on | off | status
```

### 10. Opt-in / opt-out precedence

Evaluated each invocation, highest wins:

1. `DO_NOT_TRACK` set to any non-empty, non-`0` value → off, no prompt ever
2. `DPM_TELEMETRY=on` or `DPM_TELEMETRY=off` (env)
3. `~/.config/canton-devkit/telemetry` (config file, written by `canton-devkit telemetry on|off`)
4. Default: off, prompt-on-first-operational-command if interactive

Plus `DPM_TELEMETRY_DEBUG=1` → write the JSON that would be sent to
stderr, skip the network call. Cheapest possible audit story.

### 11. CLI surface

Root-level subcommand (sibling of `localnet`, `version`), because
telemetry is tool-wide:

```
canton-devkit telemetry on       # enable, write config
canton-devkit telemetry off      # disable, write config
canton-devkit telemetry status   # show current state + precedence source
canton-devkit telemetry preview  # print this week's local counter file
```

### 12. Web UI parity

Per AGENTS.md CLI ↔ Web UI parity rule:

- Settings → Telemetry toggle
- `GET /api/telemetry` — current state + precedence source
- `POST /api/telemetry` — toggle (Origin-gated like all state-changing
  endpoints)
- `GET /api/telemetry/preview` — render this week's local counter file

Loopback-only enforced at the server level (same as everything else).

### 13. Code shape

```
internal/telemetry/
  counter.go      // Inc(chart, bucket string) — compile-time allow-list check
  allowlist.go    // var allowedCounters = map[string]map[string]struct{}{ ... }
  store.go        // weekly file at <UserConfigDir>/canton-devkit/telemetry/2026-W22.json
  uploader.go     // weekly window, 2 s timeout, no queue, 2-attempt drop
  config.go       // DO_NOT_TRACK → DPM_TELEMETRY → config file
  prompt.go       // TTY-gated first-run banner; only fires on operational localnet verbs
  debug.go        // DPM_TELEMETRY_DEBUG=1 → JSON to stderr, skip send
```

### 14. Hook point

In `internal/cli/app.go`, inside `App.Run(args []string) int`, *after*
the existing `errors.As(err, &ece)` block normalises the exit code.
This is the single chokepoint that sees both the verb and the final
process exit code — better than Cobra's `PersistentPreRun` which fires
before subcommand and can't see the exit code.

```go
func (a *App) Run(args []string) int {
    root := a.buildRoot()
    root.SetArgs(args)
    err := root.Execute()
    code := 0
    var ece localnet.ExitCodeError
    if errors.As(err, &ece) {
        code = int(ece)
    } else if err != nil {
        _, _ = fmt.Fprintln(a.err, err)
        code = 1
    }
    // Telemetry: fire-and-forget, never blocks exit, no-op if
    // disabled / DO_NOT_TRACK / non-TTY-no-config / unknown verb.
    if len(args) > 0 {
        a.telemetry.Inc("dpm/command", args[0])
        outcome := "ok"
        if code != 0 {
            outcome = "fail"
        }
        a.telemetry.Inc("dpm/command_exit", args[0]+"/"+outcome)
    }
    return code
}
```

`a.telemetry` is an interface field on `App` so the existing
`TestRunIsArgvOnly` and siblings inject a no-op fake. Preserves the
DPM-contract invariant.

### 15. Channel detection

Build-time linker flag in `release.yml`:

```yaml
- name: Build (stable)
  run: go build -ldflags "-X main.channel=stable -X main.version=${VERSION}" ./cmd/canton-devkit

- name: Build (nightly)
  if: github.ref == 'refs/heads/main' && contains(github.event.head_commit.message, '[nightly]')
  run: go build -ldflags "-X main.channel=nightly" ./cmd/canton-devkit
```

Defaults to `dev` when the linker var isn't set (local `go build` for
developers). Enum stays small: `stable | nightly | dev`; add `rc` later
only if release-candidate gating becomes a real workflow.

### 16. Domain

`telemetry.canton-devkit.dev` (subdomain), not a path on the main
site. Cleaner operationally — separate ingress/logging policy,
trivial to point at a different backend without touching the main
site, easier to enforce the "no-cookies, no-IP, no-UA" boundary at
nginx.

### 17. Tests (definition-of-done)

1. **Compile-time test** — walk every `Inc("chart", "bucket")` call
   site with `go/ast` and assert against `allowedCounters`. Catches a
   new counter slipped past review.
2. `DO_NOT_TRACK=1` → zero file writes, zero `http.Client` use.
3. `DPM_TELEMETRY_DEBUG=1` → JSON to stderr, no HTTP call.
4. Two-attempt drop: simulate upload failure twice → file rotated on
   the second failure, not the first.
5. `App.Run` injection: a fake `telemetry.Sink` records calls; verify
   only allow-listed counters fire on each subcommand path.
6. Non-TTY / `CI=true` → first-run prompt never shown.
7. Informational commands (`version`, `--help`, `telemetry status`)
   never trigger the prompt even on TTY.
8. `TestRunIsArgvOnly` still passes (the DPM-contract regression
   guard).

### 18. Public artifacts

- This design doc lives at `docs/proposals/telemetry.md` once merged.
- Allow-list, sender code, and collector code all in the public repo.
- Public dashboard at `telemetry.canton-devkit.dev` shows
  k-anonymized weekly aggregates.
- Schema changes (anything that adds or renames a counter) bump
  `schema_version` and require a PR with this doc updated.

## Implementation plan

| Phase | Scope | Ship-dark? |
|---|---|---|
| **v1.0** | `internal/telemetry/` package, allow-list, opt-in prompt, `DO_NOT_TRACK`, `App.Run` wiring, `canton-devkit telemetry` subcommand, golden tests | Yes — no collector deployed; `DPM_TELEMETRY_DEBUG=1` only |
| **v1.1** | Collector endpoint + nginx config + rollup job + public dashboard | Live |
| **v1.2** | Web UI parity (`/api/telemetry` + Settings panel) | Live |

Ship-dark means the v1.0 PR is mergeable and `canton-devkit
telemetry on` works locally (writes counters to `~/.config/.../`
and `DPM_TELEMETRY_DEBUG=1` reveals them) — but no production
collector exists yet, so even an opted-in user sees their
counters stay local until v1.1 lands.

## Open questions (not blocking design approval)

None — channel, domain, and banner timing all locked.
