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
   dpm localnet up --name dev
   ```
   Use `--version <tag>` to pin a Splice version (`dpm localnet versions`
   lists curated tags). `--name` isolates instances so several can run.

3. **Inspect**:
   ```
   dpm localnet status --name dev      # health, ports, endpoints
   dpm localnet list                   # all instances + state
   dpm localnet logs --service canton --name dev # tail one service
   ```

4. **Stop** (removes containers + volumes for that instance):
   ```
   dpm localnet down --name dev
   ```

## Guardrails
- Always run `doctor` before `up` if the user reports trouble.
- Prefer `down` for normal teardown; use `clean --name <n>` only to
  reclaim orphaned/stopped state (`--force` for a running instance).
- Never pass secrets on the command line — JWTs come from `localnet env`.
