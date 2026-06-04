# canton-devkit telemetry collector

Self-hosted ingestion endpoint for canton-devkit usage telemetry. Accepts
the CLI's per-period counter payload over HTTP and stores it in **your own
Postgres**, with **Metabase** for dashboards and one-click CSV/Excel
export. Nothing leaves your host; you own and can export the data at any
time with no vendor limits.

This is a **separate Go module** from the CLI — server-side infra, not
part of the shipped binary — so its Postgres driver never enters the CLI's
dependency graph.

## Quick start

```bash
cd telemetry-collector
POSTGRES_PASSWORD=$(openssl rand -hex 16) docker compose up -d
```

This starts:

| Service    | Port | What |
|------------|------|------|
| `postgres` | —    | your data (schema auto-applied on first boot) |
| `collector`| 8080 | the ingest endpoint (`POST /v1/counters`) |
| `metabase` | 3000 | dashboards + CSV/Excel/JSON export (open-source, AGPL) |

Point the CLI at the collector (on every machine, or bake it into release
builds via `-ldflags`):

```bash
export CANTON_DEVKIT_TELEMETRY_ENDPOINT=http://<this-host>:8080/v1/counters
```

Open Metabase at `http://<this-host>:3000`, finish its first-run setup,
add the `telemetry` Postgres as a data source, and chart `counter_period`
or the `v_command_usage` view.

## What it accepts

`POST /v1/counters`, `Content-Type: application/json`, body:

```json
{
  "schema_version": 2,
  "period": "2026-06-04",
  "granularity": "daily",
  "counters": { "dpm/command": { "up": 5, "down": 2 }, "dpm/os": { "darwin": 7 } }
}
```

- `period` — `YYYY-MM-DD` (daily) or `YYYY-Www` (weekly). The legacy v1
  `"week"` key is accepted as a fallback.
- `granularity` — `daily` | `weekly` (inferred from the key shape if
  omitted by an old client).
- Returns **204** on success, **400** on a malformed body, **401** if
  `INGEST_TOKEN` is set and the `X-Telemetry-Token` header doesn't match,
  **500** on a storage error (the DB error is logged, never returned).

**Idempotent upsert:** the CLI sends cumulative totals for a period, so a
re-send of the same period **replaces** the prior counts (last write
wins), keyed on `(period, chart, bucket)`.

## Schema

One row per `(period, chart, bucket)` — see [`schema.sql`](schema.sql).
`period_date` is the start-of-period date (daily → the date; weekly → the
ISO Monday) so Metabase can plot a clean time series regardless of
granularity.

## Export your data (no limits)

```bash
# Full logical backup
docker compose exec postgres pg_dump -U postgres telemetry > backup.sql

# CSV of all counters
docker compose exec postgres psql -U postgres -d telemetry \
  -c "\copy (SELECT * FROM counter_period) TO STDOUT CSV HEADER" > counters.csv
```

Or use Metabase's per-chart **Export → CSV / Excel / JSON**.

## Config

| Env             | Default     | Purpose |
|-----------------|-------------|---------|
| `DATABASE_URL`  | (required)  | Postgres DSN |
| `LISTEN_ADDR`   | `:8080`     | listen address |
| `INGEST_TOKEN`  | (unset)     | when set, require it in `X-Telemetry-Token` |

## Privacy

The payload is zero-PII by construction (the CLI sends only allow-listed
counter names, coarse period buckets, and integers — no IDs, no paths, no
timestamps finer than the period). The collector stores exactly that and
adds nothing. It never logs request bodies.

## Tests

```bash
go test ./...        # handler tests; no database required (fake store)
```

End-to-end (collector → real Postgres) is exercised against a throwaway
`postgres:16` container; see the PR description for the verified run.
