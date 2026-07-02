# Zero-to-LocalNet validation checklist

The goal: **a new developer reaches a running LocalNet in under 10
minutes.** Use this checklist to validate your installation after
installing canton-devkit, or to sanity-check a release candidate on a
fresh machine.

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

## Manual checklist

A first-time user with Docker installed should be able to tick every
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

## Reporting results

If a step fails or blows the budget, please open an issue. These details
make a run reproducible:

| Field | Example |
|---|---|
| Platform | macOS 14 arm64 / Ubuntu 22.04 amd64 |
| Docker memory | 8 GiB |
| Cold or warm cache | cold |
| `up` wall-clock | 6m12s |
| Result | pass / fail (step) |
| Friction notes | "doctor memory hint was clear"; "didn't know which port was the UI" |

Successful timings are welcome too — they help track how the
zero-to-LocalNet experience holds up across platforms.
