# Zero-to-LocalNet validation checklist

The M1 adoption bar is: **a new developer reaches a running LocalNet in
under 10 minutes.** This page is the reviewer-facing checklist behind that
metric. Run it yourself before a release, or hand it to an external
reviewer (see [adoption/reviewer-kit.md](adoption/reviewer-kit.md)).

## Automated harness

```bash
# default 10-minute budget
scripts/validate-zero-to-localnet.sh

# true cold start (clears the Splice cache first → includes the ~140 MB download)
COLD=1 scripts/validate-zero-to-localnet.sh

# looser budget on a slow link
BUDGET_SECONDS=900 scripts/validate-zero-to-localnet.sh
```

Exit `0` = passed within budget · `1` = a step failed · `2` = over budget.
The harness times: binary present → `doctor` → `up` (the long pole) →
`status` healthy → teardown.

## Manual reviewer checklist

A first-time reviewer with Docker installed should be able to tick every
box without reading source:

- [ ] **Install** — one command from [getting-started.md](getting-started.md)
      (DPM component, `install.sh`, or a release binary) puts
      `canton-devkit` / `dpm` on `PATH`.
- [ ] **Doctor** — `localnet doctor` runs and clearly reports any host gap
      (Docker down, low memory, missing compose v2) with a fix.
- [ ] **Up** — `localnet up --name demo` downloads (on first run), boots,
      waits for readiness, and prints endpoints. **Wall-clock < 10 min.**
- [ ] **Status** — `localnet status --name demo` shows healthy services +
      participant/UI endpoints.
- [ ] **UI** — `localnet ui` opens a dashboard at the printed URL.
- [ ] **Down** — `localnet down --name demo` stops cleanly; `localnet list`
      reflects it.
- [ ] **No surprises** — no manual Docker commands, no editing config
      files, no hunting for ports.

## What to record

For each reviewer / run, capture:

| Field | Example |
|---|---|
| Platform | macOS 14 arm64 / Ubuntu 22.04 amd64 |
| Docker memory | 8 GiB |
| Cold or warm cache | cold |
| `up` wall-clock | 6m12s |
| Result | pass / fail (step) |
| Friction notes | "doctor memory hint was clear"; "didn't know which port was the UI" |

Aggregate these in the adoption transparency update (M4). Three external
reviewers passing the manual checklist satisfies the M1 adoption metric.
