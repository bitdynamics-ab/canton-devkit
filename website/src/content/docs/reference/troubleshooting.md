---
title: "Troubleshooting"
description: "Failure modes and fixes for LocalNet bring-up, ports, V2 token instances, credentials, and snapshots."
---

Failure modes and fixes. Start with `canton-devkit localnet doctor
--name <instance>` — it checks Docker, memory, ports, version channel,
and the alpha-profile requirement, and prints targeted remediation.

## `localnet up` fails or containers OOM-loop

**Symptom:** Canton container restarts repeatedly; `up` times out.

**Cause:** Docker memory below the version's floor. Splice 0.6.x needs
≈8 GiB; the V2 alpha similar. The default Docker Desktop allocation
(4 GiB) is too low.

**Fix:** Raise Docker memory to the recommended value (`doctor` prints
it), then `localnet up` again. The per-version preflight gate surfaces
this before the stack starts.

## Port already in use

**Symptom:** `PORTS_IN_USE` error envelope on `up`.

**Fix:** Another instance (or a stale container) holds the port block.
`localnet list` to find it, `localnet down --name <other>` to free it, or
pass a different `--name` (each name gets its own block). Note: Docker may
reassign ephemeral host ports across a restart — re-read them from
`localnet status` rather than caching old values.

## V2 instance: ledger port refused / scan registry 502

**Symptom:** token commands fail with `connection refused` on the
participant port, or Amulet transfers return `HTTP 502` from nginx.

**Cause:** The V2 alpha image ships a broken in-container healthcheck, so
the Splice container can read `health: starting` for a long time even
when functional — and the off-ledger scan registry (behind nginx) isn't
ready until the Splice app fully boots.

**Fix:**
- Give the stack more time; `doctor` / `status` reflect real readiness
  via the readyz fallback, not just the container healthcheck.
- The **native test-token** path (your own `splice-test-token-v2`
  instrument) needs **no scan registry** — its `TokenRules` is the
  registry — so create/mint/transfer/burn of your own token work even
  while the scan app is still coming up. Only **Amulet** transfers depend
  on the scan registry.
- If the participant port is genuinely down, `localnet status` will show
  it; restart with `localnet restart --name <i>`.

## Token: "package not vetted" / manual DAR upload

**Symptom:** `token create` errors that `splice-test-token-v2` isn't
vetted.

**Fix:** `token create --endpoint …` auto-fetches and uploads the
test-token + burn-mint DARs (pinned to the instance's Splice commit). If
you're offline or the fetch fails, upload them manually with
`localnet dar upload <dar>` and retry.

## Token: mint/burn disabled in the Web UI

Mint and Burn are gated to **native CIP-0112 v2 instruments created on
this instance**. Amulet and registry-only ("recorded") instruments have
no mint/burn surface — create your own token to exercise them.

## Credentials lost after a failed `up`

**Symptom:** token/ledger commands can't find a JWT for a role.

**Fix:** `localnet creds --name <i> --role <role> --format raw`
re-issues a dev token from the project's env files. The token commands
also auto-issue per-role tokens when `--token` is empty.

## Snapshot consistency

`localnet snapshot` captures Docker volumes + registry state. For a
**running** instance this is a crash-consistent (not
application-consistent) copy: in-flight transactions or unflushed
database writes may not be fully captured. For a guaranteed-consistent
snapshot, `localnet down --name <i>` first, then snapshot. `snapshot`
warns when run against a running instance.

## Still stuck?

- `localnet logs --name <i> [service]` — tail container logs.
- `localnet doctor --name <i>` — host + instance diagnostics.
- File a [GitHub issue](https://github.com/bitdynamics-ab/canton-devkit/issues)
  with the `doctor` output and the failing command.

## Log lookup implementation note

`localnet logs` intentionally asks Docker Compose for logs by project
label only (`docker compose -p <project> logs`). It does not replay the
cached `compose.yaml` files, generated overlays, or `--env-file` list
from registry state. Logs are read from already-created containers, so
rebuilding the active Compose model is unnecessary and can hide
profile-gated services unless the exact profile set from `localnet up`
is replayed.
