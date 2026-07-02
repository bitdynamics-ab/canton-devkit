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
cp .env.example .env          # set POSTGRES_PASSWORD
docker compose up -d --build
```

> **Deploying for real** — local testing vs production, TLS, secrets,
> backups, and baking the endpoint into release binaries: see
> **[DEPLOY.md](DEPLOY.md)**.

This starts:

| Service    | Port | What |
|------------|------|------|
| `postgres` | —    | your data (schema auto-applied on first boot) |
| `collector`| 8080 | the ingest endpoint (`POST /v1/counters`) |
| `metabase` | 3000 | dashboards + CSV/Excel/JSON export (open-source, AGPL) |

Point the CLI at the collector (on every machine, or bake it into release
builds via `-ldflags`):

```bash
# Local/dev stack:
export CANTON_DEVKIT_TELEMETRY_ENDPOINT=http://<this-host>:8080/v1/counters
# Hosted collector used by official release builds:
# export CANTON_DEVKIT_TELEMETRY_ENDPOINT=https://canton-devkit-telemetry.bitdynamics.me/v1/counters
```

Open Metabase at `http://<this-host>:3000` and finish its first-run
signup. Then provision the data source + dashboards in one shot:

```bash
# create an API key in Metabase: Admin → Settings → API Keys (Admin group)
MB_API_KEY='mb_...' DB_PASS="$POSTGRES_PASSWORD" python3 setup-metabase.py
# or with email+password instead of a key:
MB_USER='you@example.com' MB_PASS='...' DB_PASS="$POSTGRES_PASSWORD" python3 setup-metabase.py
```

[`setup-metabase.py`](setup-metabase.py) is idempotent (re-runs update in
place) and stdlib-only. It connects the `telemetry` database, creates a
**canton-devkit telemetry** collection, and assembles a `canton-devkit
usage` dashboard grouped into topic sections, each chart tagged by source
(`[telemetry]` / `[GitHub]` / `[qualitative]`):

- **LocalNet lifecycle** — commands/day, LocalNet start ok-vs-fail,
  platform split (incl. Windows), top commands
- **Web UI & tooling** — Web UI features used, CI-vs-interactive,
  AI-agent usage
- **Token flows** — token actions (the CIP-0112 create → mint → transfer
  flow)
- **Adoption** — cumulative downloads, stars/forks, and the
  **`adoption_evidence`** table — externally reported usage you log by
  hand (the one signal neither telemetry nor GitHub can provide)

Works the same against a production Metabase by setting `MB_URL`.

Prefer the UI? You can also add the `telemetry` Postgres as a data source
manually and chart `counter_period` / the `v_command_usage` view.

## GitHub adoption signals (downloads, stars/forks)

Telemetry is zero-PII, so it **can't count unique installs**. Install and
visibility signals come from GitHub instead.
[`cmd/github-stats`](cmd/github-stats) snapshots release-asset download
counts and repo stars/forks/watchers into the same Postgres (tables
`github_release_downloads`, `github_repo_stats`, view
`v_downloads_total`), so Metabase can chart the cumulative-download trend
and the visibility curve.

Run it daily (cron / GitHub Action — see
[`deploy/github-stats.cron.yml`](deploy/github-stats.cron.yml)):

```bash
DATABASE_URL=postgres://… GITHUB_REPO=owner/name GITHUB_TOKEN=ghp_… \
  go run ./cmd/github-stats
```

`setup-metabase.py` adds the **Cumulative downloads** and **Stars & forks**
charts to the dashboard automatically.

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

**Fleet aggregation:** the fleet is many machines reporting the same
period (telemetry is zero-PII, so there's no machine identifier to dedup
by). Ingestion **sums** on conflict, keyed on `(period, chart, bucket)` —
machine A's `up=5` and machine B's `up=3` for the same day aggregate to
`8`. (Tradeoff: a machine whose upload committed but whose response was
lost over-counts that one period by one cycle on its deferred retry —
rare, bounded, negligible for adoption trends.)

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

End-to-end coverage (collector → real Postgres) lives in
[`aggregate_integration_test.go`](aggregate_integration_test.go); it is
skipped unless `TEST_DATABASE_URL` points at a Postgres (e.g. a throwaway
`postgres:16` container) with `schema.sql` applied.
