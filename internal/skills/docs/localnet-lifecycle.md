---
name: canton-localnet-lifecycle
description: Start, inspect, and stop a Canton LocalNet with canton-devkit. Use when the user wants a local Canton/Daml network to develop or test against.
---

# Canton LocalNet lifecycle

Manage a local Canton network using `dpm localnet` (or `canton-devkit
localnet` standalone). Every command is idempotent and returns
deterministic exit codes.

## When to use
The user asks to "spin up Canton locally", "start a LocalNet", "check
if the network is healthy", or "tear it down".

## Safe workflow

1. **Check the host first** (never modifies anything):
   ```
   dpm localnet doctor
   ```
   Exit 0 = ready; exit 2 = a prerequisite failed (it prints how to fix).

2. **Start a named instance** (blocks until healthy):
   ```
   dpm localnet up dev
   ```
   Use `--version <tag>` to pin a Splice version (`dpm localnet versions`
   lists curated tags). The instance name isolates instances so several
   can run.

3. **Inspect**:
   ```
   dpm localnet status dev             # health, ports, endpoints
   dpm localnet list                   # all instances + state
   dpm localnet logs dev --service canton  # tail one service
   ```

4. **Pause or stop without removing containers** (fast to resume):
   ```
   dpm localnet pause dev              # freeze in place; resume with `resume`
   dpm localnet resume dev             # thaw a paused instance (also: unpause)
   dpm localnet stop dev               # graceful stop; restart with `start`
   dpm localnet start dev              # start a stopped instance
   ```
   `pause`/`resume` freeze containers (RAM held, CPU freed).
   `stop`/`start` stop the containers (CPU and runtime freed) but keep
   them on disk, so `start` skips stack recreation. `start` on an
   instance whose containers were already removed falls back to a full
   `up` automatically.

5. **Tear down** (stops and removes containers; data volumes kept):
   ```
   dpm localnet down dev
   ```

## Guardrails
- Always run `doctor` before `up` if the user reports trouble.
- Reach for the lightest teardown that fits: `pause` for a short break,
  `stop` for a clean shutdown you'll resume soon, `down` to free
  container resources, `remove --name <n>` (alias: `clean`) only to
  reclaim data volumes and registry state (`--force` for a running
  instance).
- Never pass secrets on the command line — JWTs come from `localnet env`.
- The instance name can be passed as a positional arg (`localnet up dev`)
  or via `--name` (`localnet up --name dev`). Both forms are equivalent.
