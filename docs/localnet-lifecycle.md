# LocalNet Lifecycle

Canton DevKit is a single-binary developer tool for running and operating a
Canton **LocalNet** — a full local Canton Network (sequencers, mediators,
participants, Splice apps) in Docker. It gives you a CLI
(`canton-devkit localnet …`, or `dpm localnet …` under DPM) and an
embedded Web UI for the same operations.

This guide walks the full lifecycle: bring an instance up, inspect it,
run several at once, and clean up. See
[Installation & Getting Started](getting-started.md) first if you
haven't installed DevKit yet.

## Zero to running LocalNet

```bash
# 1. Check the host (no changes made)
canton-devkit localnet doctor

# 2. Start a named LocalNet (downloads Splice on first run; waits for readiness)
canton-devkit localnet up --name demo

# 3. Inspect it — endpoints, health, credentials
canton-devkit localnet status --name demo

# 4. Export endpoints for your app/tests
eval "$(canton-devkit localnet env --name demo)"

# 5. Upload a DAR
canton-devkit localnet dar upload ./my-app.dar --instance demo

# 6. Watch live contracts. The participant gRPC endpoint isn't
#    host-published by default, so pass --endpoint host:port
#    (auto-discovery from --name is not yet supported). Find the
#    port under "participant_ledger_app-user" in `status` output.
canton-devkit localnet contracts watch --name demo --endpoint localhost:<ledger-port>

# 7. Tear it down
canton-devkit localnet down --name demo
```

Replace `canton-devkit` with `dpm` if you installed via the DPM
component. `up` waits for the stack to become healthy (Splice
onboarding can take several minutes on a cold start) and prints the
service endpoints and credential locations when ready.

## Running two LocalNets at once

```bash
canton-devkit localnet up --name alpha
canton-devkit localnet up --name beta
canton-devkit localnet list           # both instances + their state
```

Each named instance gets its own deterministic compose project,
network, and host ports, so they don't collide.

### Explicit, deterministic ports (`--port-base`)

By default DevKit **auto-allocates** host ports — the simplest path, and
it never conflicts because the kernel hands out free ports. When you need
a **fixed, predictable** port map instead — reproducible CI layouts, or
multiple instances at known offsets — pin a base:

```bash
canton-devkit localnet up --name alpha --port-base 20000   # services at 20000+N
canton-devkit localnet up --name beta  --port-base 30000   # services at 30000+N
```

Each service lands on `base + N`, identically across runs and machines.
Every derived port must be free or `up` fails fast (no silent fallback) —
so the layout you asked for is the layout you get. Pre-flight a base
before bringing anything up:

```bash
canton-devkit localnet doctor --port-base 20000   # are 20000..20000+services free?
```

The same control is available in the Web UI's **New instance** dialog
under *Advanced → Fixed port base*.

## Pause, stop, or tear down

DevKit gives you three ways to make an instance stop doing work, each
trading resource savings against restart cost. All three have symmetric
"undo" commands and identical Web UI buttons on the instance detail card.

| Command | What it does | Containers | Volumes/state | Resume with | Restart cost |
| --- | --- | --- | --- | --- | --- |
| `localnet pause` | Freezes containers in place (`docker compose pause`) | Kept, paused | Kept | `localnet resume` (alias `unpause`) | Instant — processes thaw |
| `localnet stop` | Gracefully stops containers (`docker compose stop`) | Kept, stopped | Kept | `localnet start` | Fast — containers restart |
| `localnet down` | Stops **and removes** containers (`docker compose down`) | Removed | Kept | `localnet up` | Slow — recreates the stack |

Notes:

- **Pause** holds RAM (containers still resident) but frees CPU — best
  for a short break where you want to jump straight back in.
- **Stop** releases both CPU and the container runtime while keeping the
  containers on disk, so `start` skips image pulls and stack recreation.
  If an instance is paused, resume or unpause it before stopping.
- **Down** frees everything except your data volumes; `up` rebuilds the
  stack from the recorded version and profiles. `localnet start` on an
  instance whose containers are already gone transparently falls back to
  a full `up` for you.
- `localnet clean` (below) is the only command that removes **data
  volumes and registry state** — it is not part of the reversible set.

**Most common choices:**

- Stepping away for a few minutes → `pause` / `resume`.
- Done for the day, want a fast start tomorrow → `stop` / `start`.
- Freeing the machine or resetting the containers → `down` / `up`.
- Throwing the instance away entirely → `clean`.

## Uninstall / clean up

```bash
# stop + remove a single instance's containers, volumes, and state
canton-devkit localnet clean --name demo

# remove every DevKit-managed instance
canton-devkit localnet clean --all

# remove the standalone binary
sudo rm /usr/local/bin/canton-devkit
```

`clean` refuses to touch a running instance unless you pass `--force`
(which tears it down first). Use `--dry-run` to preview.

For common questions, see the [FAQ](faq.md).
