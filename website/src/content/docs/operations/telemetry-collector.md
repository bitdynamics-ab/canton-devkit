---
title: "Telemetry Collector — Deploy Overview"
description: "Self-host the ingestion endpoint for canton-devkit usage telemetry: a Go collector, your own Postgres, and Metabase dashboards, deployed with Docker Compose."
---

The telemetry collector is the self-hosted ingestion endpoint for
canton-devkit usage telemetry. It accepts the CLI's per-period counter
payload over HTTP and stores it in **your own Postgres**, with
**Metabase** for dashboards and one-click CSV/Excel export. Nothing
leaves your host; you own and can export the data at any time with no
vendor limits.

It is a **separate Go module** from the CLI — server-side infra, not
part of the shipped binary — so its Postgres driver never enters the
CLI's dependency graph. The module lives at
[`telemetry-collector/`](https://github.com/bitdynamics-ab/canton-devkit/tree/main/telemetry-collector)
in the repository.

:::note
This page is an operator-facing overview. The authoritative, always-current
instructions live in the repo:
[`telemetry-collector/README.md`](https://github.com/bitdynamics-ab/canton-devkit/blob/main/telemetry-collector/README.md)
(quick start, Metabase provisioning, GitHub adoption signals) and
[`telemetry-collector/DEPLOY.md`](https://github.com/bitdynamics-ab/canton-devkit/blob/main/telemetry-collector/DEPLOY.md)
(local testing vs production, TLS, secrets, backups, baking the endpoint
into release binaries).

For what the CLI records and how users opt out, see the
[Telemetry reference](../../reference/telemetry/).
:::

## The stack

```
 dpm CLI  ──HTTPS POST──▶  collector :8080  ──▶  Postgres  ◀──  Metabase :3000
 (release binaries)        (this module)        (your data)     (dashboards/export)
```

| Service    | Port | What |
|------------|------|------|
| `postgres` | —    | your data (schema auto-applied on first boot) |
| `collector`| 8080 | the ingest endpoint (`POST /v1/counters`) |
| `metabase` | 3000 | dashboards + CSV/Excel/JSON export (open-source, AGPL) |

## Quick start (local testing)

```bash
cd telemetry-collector
cp .env.example .env          # set POSTGRES_PASSWORD
docker compose up -d --build
```

Then point a CLI at the collector:

```bash
export CANTON_DEVKIT_TELEMETRY_ENDPOINT=http://<this-host>:8080/v1/counters
export DPM_TELEMETRY=on
dpm localnet list                          # records counters locally
dpm telemetry flush                        # ship them to the collector now (normally only completed days upload)
```

Open Metabase at `http://<this-host>:3000`, finish its first-run signup,
and provision the data source + dashboards in one shot with the
idempotent, stdlib-only
[`setup-metabase.py`](https://github.com/bitdynamics-ab/canton-devkit/blob/main/telemetry-collector/setup-metabase.py)
script.

## Production deployment

For any internet-facing deployment, run the base compose **with the
production override**:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

The override hardens the dev stack:

- Binds every published port to `127.0.0.1` only (the host reverse proxy
  is the sole public entrypoint) and stops publishing Postgres to the
  host entirely.
- Configures the always-on in-process rate limiter for a proxied
  deployment: per-IP keying reads `X-Forwarded-For` (set by the front
  nginx) and the `RATE_*` knobs become tunable.
- Exposes the collector on `127.0.0.1:${COLLECTOR_PORT:-8090}` so it can
  sit beside other services on a shared host.

Key production requirements covered in
[DEPLOY.md](https://github.com/bitdynamics-ab/canton-devkit/blob/main/telemetry-collector/DEPLOY.md):

- **TLS is required** — the CLI should POST over HTTPS. Terminate TLS at
  a reverse proxy (e.g. Caddy with automatic Let's Encrypt) in front of
  the collector's plain-HTTP `:8080`.
- **Edge protection and rate limiting** before any public exposure.
- **Where to host** — the data volume is tiny (weekly/daily aggregates),
  so a single small VM (2 vCPU / 4 GB) runs the whole stack comfortably.
  Recommended baseline: one VM, this compose, a reverse proxy for TLS,
  nightly `pg_dump` to object storage.

## GitHub adoption signals

Usage telemetry counts distinct installs only via an anonymous random
token (see the [Telemetry reference](../../reference/telemetry/)).
GitHub adds complementary adoption signals:
[`cmd/github-stats`](https://github.com/bitdynamics-ab/canton-devkit/tree/main/telemetry-collector/cmd/github-stats)
snapshots release-asset download counts and repo stars/forks/watchers
into the same Postgres, alongside the counter data.
