# Troubleshooting

Failure modes and fixes. Start with `canton-devkit localnet doctor` —
it runs the same host preflight as `localnet up` (Docker CLI, daemon,
Compose v2, disk + memory headroom, platform, port availability; pass
`--version <tag>` to use that version's memory thresholds) and prints
targeted remediation.

## `localnet up` fails or containers OOM-loop

**Symptom:** Canton container restarts repeatedly; `up` times out.

**Cause:** Docker memory below the version's floor. Splice 0.6.x needs
≈8 GiB. Docker Desktop defaults to allocating
50% of host memory, which on smaller machines lands below that floor.

**Fix:** Raise Docker memory to the recommended value (`doctor` prints
it), then `localnet up` again. The per-version preflight gate surfaces
this before the stack starts.

## Port already in use

**Symptom:** `PORTS_IN_USE` error envelope on `up`.

**Fix:** Another instance (or a stale container) holds the port block.
`localnet list` to find it, `localnet down <other>` to free it, or
pass a different instance name (each name gets its own block). Note: Docker may
reassign ephemeral host ports across a restart — re-read them from
`localnet status` rather than caching old values.

## Legacy V2 alpha instance: ledger port refused / scan registry 502

**Symptom:** token commands fail with `connection refused` on the
participant port, or Amulet transfers return `HTTP 502` from nginx.

**Cause:** The legacy V2 alpha image ships a broken in-container healthcheck, so
the Splice container can read `health: starting` for a long time even
when functional — and the off-ledger scan registry (behind nginx) isn't
ready until the Splice app fully boots.

**Fix:**
- Give the stack more time; the readiness wait in `up` / `start` /
  `restart` treats the validator's `/api/validator/readyz` returning
  200 as ready even while Docker still reports `health: starting`.
  `localnet status` renders Docker's reported health (a container in
  `health: starting` shows as `syncing`), so `syncing` there does not
  necessarily mean broken — and `doctor` checks the host only and
  never probes instances.
- The **native test-token** path (your own `splice-test-token-v2`
  instrument) needs **no scan registry** — its `TokenRules` is the
  registry — so create/mint/transfer/burn of your own token work even
  while the scan app is still coming up. Only **Amulet** transfers depend
  on the scan registry.
- If the participant port is genuinely down, `localnet status` will show
  it; restart with `localnet restart <i>`.

## Token: "package not vetted" / manual DAR upload

**Symptom:** `token create` errors that `splice-test-token-v2` isn't
vetted.

**Fix:** `token create --instance <i> --endpoint <host:port>`
auto-fetches and uploads the test-token + burn-mint DARs (pinned to
the instance's Splice commit). If you're offline or the fetch fails,
upload them manually with
`localnet dar upload <dar> --instance <i> --all-participants` and
retry.

## Token: mint/burn disabled in the Web UI

Mint and Burn are gated to **native CIP-0112 v2 instruments created on
this instance**. Amulet and registry-only ("recorded") instruments have
no mint/burn surface — create your own token to exercise them.

## Credentials lost after a failed `up`

**Symptom:** token/ledger commands can't find a JWT for a role.

**Fix:** `localnet creds <i> --role <role> --format raw`
prints the JWT captured at `up` time from `state.json`. If no
credentials were captured (e.g. the `up` failed before JWT capture),
re-run `localnet up` to completion. The token commands also auto-issue
per-role tokens when `--token` is empty.

## Snapshot consistency

`localnet snapshot --to <file.tgz>` captures a logical `pg_dumpall` of
the instance's Postgres plus registry state. The instance must be
**running** — the dump reads from live Postgres, so a stopped instance
cannot be snapshotted. Node containers are paused for the duration of
the dump, so the snapshot is application-consistent; there is no need
to `down` first.

## Still stuck?

- `localnet logs <i> [--service <svc>]` — tail container logs
  (repeat `--service` to filter to specific services).
- `localnet doctor` — host readiness diagnostics (docker, resources,
  network); use `localnet status <i>` for per-instance state.
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
