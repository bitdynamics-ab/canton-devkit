# Deploying the telemetry collector

Two scenarios: **local testing** (your dev machine) and **production /
mainnet release** (a host the released CLI fleet phones home to). Same
compose stack, different hardening.

The stack is three containers:

```
 dpm CLI  ──HTTPS POST──▶  collector :8080  ──▶  Postgres  ◀──  Metabase :3000
 (release binaries)        (this module)        (your data)     (dashboards/export)
```

---

## 1. Local testing

On your dev machine, with Docker running:

```bash
cd telemetry-collector
cp .env.example .env
# edit .env: set POSTGRES_PASSWORD (any value for local)
docker compose up -d --build
```

Wait ~1 min for Metabase's first boot, then:

```bash
# collector is up
curl -s localhost:8080/healthz            # → ok

# point a LOCAL CLI build at it and exercise it
export CANTON_DEVKIT_TELEMETRY_ENDPOINT=http://localhost:8080/v1/counters
export DPM_TELEMETRY=on
dpm localnet list                          # records + ships counters

# see the rows
docker compose exec postgres psql -U postgres -d telemetry \
  -c "SELECT * FROM counter_period ORDER BY received_at DESC;"
```

**Metabase first-run** (one-time, manual — it's an interactive web setup):

1. Open `http://localhost:3000`, create the admin account.
2. *Add a database* → PostgreSQL → host `postgres`, port `5432`, db
   `telemetry`, user `postgres`, your password.
3. Build questions on `counter_period` / `v_command_usage`. Every chart
   has **Export → CSV / Excel / JSON**.

Tear down (and wipe data): `docker compose down -v`.

---

## 2. Production / mainnet release

### 2a. The production override + our hosted instance

For any internet-facing deployment, run the base compose **with the
production override**:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

The override (`docker-compose.prod.yml`) hardens the dev stack:

- Binds every published port to `127.0.0.1` only (the host reverse proxy is
  the sole public entrypoint) and stops publishing Postgres to the host
  entirely.
- Turns on the in-process rate limiter, keyed off `X-Forwarded-For` (set by
  the front nginx).
- Exposes the collector on `127.0.0.1:${COLLECTOR_PORT:-8090}` so it can sit
  beside other services on a shared host.

> **Our hosted instance (`canton-devkit-telemetry.bitdynamics.me`).** The
> nginx vhost, TLS/certbot scripts, the `.env`, and the deployment runbook for
> the instance we operate do **not** live in this repo — they live in the
> **`canton-infra`** repo under **`telemetry/`** (tracked as task 024). That
> repo holds only the server/deploy glue; the collector code and compose
> files stay here in `canton-devkit` as the single source of truth. The
> server checks out both repos and runs this compose with `--env-file`
> pointing at `canton-infra/telemetry/.env`. See `canton-infra/telemetry/README.md`.

The rest of this section documents the generic self-host path (your own VM,
your own proxy) for anyone running their own collector.

### 2a-bis. Where to host

The data volume is tiny (weekly/daily aggregates from N machines), so a
**single small VM runs the whole stack comfortably**:

| Option | Notes |
|---|---|
| **One VPS** (Hetzner CX22, DigitalOcean, Lightsail, Fly.io) | Cheapest. Run this exact compose. 2 vCPU / 4 GB is plenty. |
| **Managed Postgres + container host** | Point `DATABASE_URL` at RDS/Cloud SQL/Neon; run only the `collector` (and optionally Metabase) as containers. Best if you want managed backups/HA. |
| **Kubernetes** | Overkill for this volume, but the image is a plain stateless HTTP server if you already have a cluster. |

Recommended baseline: **one VM, this compose, a reverse proxy for TLS,
nightly `pg_dump` to object storage.**

### 2b. TLS (required — the CLI should POST over HTTPS)

Put a reverse proxy in front so the public endpoint is
`https://telemetry.yourdomain.tld`. The collector itself speaks plain
HTTP on `:8080`; terminate TLS at the proxy. Minimal **Caddy** (auto Let's
Encrypt) — add to the compose or run on the host:

```caddyfile
telemetry.yourdomain.tld {
    reverse_proxy localhost:8080
}
# Optional: gate Metabase behind the same cert on a subdomain.
metabase.yourdomain.tld {
    reverse_proxy localhost:3000
}
```

Then **don't** publish `:8080` / `:3000` to the public internet directly —
bind them to `127.0.0.1` in compose (`"127.0.0.1:8080:8080"`) so only the
proxy reaches them.

#### Edge protection & rate limiting (do this before ANY public exposure)

The most important DDoS control is **at the edge, not in the app** — by
the time a volumetric flood reaches your VM, the bandwidth is already
spent. This is also how anonymous-telemetry endpoints are protected in
practice (Next.js posts to a Vercel edge function; you can't authenticate
a client whose credential ships inside a public binary, so the defense is
edge + validation, not client auth).

**Recommended: a Cloudflare Tunnel (free).** The origin opens an
outbound-only connection to Cloudflare, so there are **zero open inbound
ports**, the origin IP is hidden, TLS is automatic, and Cloudflare absorbs
L3/L4 DDoS before traffic reaches you:

```bash
# on the collector host
cloudflared tunnel login
cloudflared tunnel create canton-telemetry
# route a hostname to the local collector (still bound to 127.0.0.1:8080)
cloudflared tunnel route dns canton-telemetry telemetry.yourdomain.tld
cloudflared tunnel run --url http://127.0.0.1:8080 canton-telemetry
```

Then in the Cloudflare dashboard add a **Rate Limiting rule** scoped to
the **whole hostname** `telemetry.yourdomain.tld` (all paths, not just
`/v1/counters`) — e.g. *10 requests / minute / IP → block for 1 minute*. A
real client POSTs ~once per day, so this is far above any legitimate use.
(The collector also 404s every path except `/v1/counters` and `/healthz`,
so a host-wide rule can't be sidestepped by POSTing to another path.)

If you prefer Caddy/nginx instead of Cloudflare, you get TLS but **not**
upstream DDoS scrubbing — the flood still hits your bandwidth. Cloudflare
(or any CDN/WAF) is the meaningfully stronger posture for a public
endpoint.

#### In-process rate limiting (defense-in-depth, ON by default)

The collector also rate-limits itself — this protects Postgres from write
amplification and bounds anything that slips past the edge. Tune via env
(defaults shown):

```bash
RATE_PER_IP_PER_MIN=30    # sustained requests/min per client IP
RATE_BURST=15             # per-IP burst allowance
RATE_GLOBAL_PER_SEC=50    # ceiling on total accepted req/s (protects the DB)
```

Over-limit requests get `429 Too Many Requests` + `Retry-After` (and a
sampled log line so an attack is visible). `/healthz` is never limited.

**Per-IP keying — set `TRUSTED_IP_HEADER` to match your front.** A client
can forge any header, so the limiter trusts **none** by default and keys
off the transport peer. Tell it which header carries the real client IP
for *your* deployment:

```bash
# Behind a Cloudflare Tunnel — CF sets (and overwrites) this, so it can't
# be spoofed:
TRUSTED_IP_HEADER=CF-Connecting-IP

# Behind a single Caddy/nginx that appends the client to X-Forwarded-For
# (the limiter takes the LAST hop, which the client can't forge):
TRUSTED_IP_HEADER=X-Forwarded-For

# Directly exposed (no proxy) — leave UNSET; the peer address is used and
# spoofed headers are ignored.
```

If you leave it unset behind a proxy, per-IP limiting degrades to "all
traffic under the proxy's IP" (i.e. effectively the global cap only) —
safe, just coarser. Setting the *wrong* header for your topology is the
only way to reintroduce spoofing, so match it to your front.

#### On `INGEST_TOKEN` (it's a speed bump, not auth)

`INGEST_TOKEN` (§2c) gates on a shared header, but since any token would
ship inside the public CLI binary it can't be a real secret — treat it
like a casual-scanner deterrent, the way PostHog's public project key or
an App Insights instrumentation key works. The load-bearing controls are
the edge rate-limit + the in-process limiter + strict payload validation,
**not** the token. (If you ever switch to a known, opt-in tester cohort,
issue per-tester credentials via Cloudflare Access instead.)

### 2c. Secrets

In `.env` on the host (never commit it):

```bash
POSTGRES_PASSWORD=$(openssl rand -hex 24)
INGEST_TOKEN=$(openssl rand -hex 24)     # optional; see note below
```

`INGEST_TOKEN` makes the collector require `X-Telemetry-Token`. The CLI
doesn't send that header yet, so for the first rollout leave it **empty**
and rely on: (a) HTTPS, (b) the endpoint being zero-PII anyway. If you
later want authenticated ingest, that's a small CLI follow-up (add a
`CANTON_DEVKIT_TELEMETRY_TOKEN` env → header); the collector side is
already done.

### 2d. Bake the endpoint into release binaries

**This is the only CLI-side step, and the release workflow is already
wired for it.** `.github/workflows/release.yml` builds stable binaries
with:

```
-ldflags "... -X github.com/bitdynamics-ab/canton-devkit/internal/telemetry.endpoint=${TELEMETRY_ENDPOINT}"
```

where `TELEMETRY_ENDPOINT` comes from the repo variable `vars.TELEMETRY_ENDPOINT`.
So to turn on telemetry for the released ("mainnet") fleet:

1. **Settings → Secrets and variables → Actions → Variables → New
   repository variable**
   - Name: `TELEMETRY_ENDPOINT`
   - Value: `https://telemetry.yourdomain.tld/v1/counters`
2. Cut a release tag. The published binaries now POST there by default.

Leave the variable **unset** and binaries ship "dark" (counters spool
locally, nothing sent) — which is the current state. Users can always
override or disable at runtime: `CANTON_DEVKIT_TELEMETRY_ENDPOINT=...`,
`dpm telemetry off`, `DPM_TELEMETRY=off`, or `DO_NOT_TRACK=1`.

### 2e. Backups (your "export when I need", automated)

The data lives in the `pgdata` Docker volume. Nightly logical backup:

```bash
# cron: 0 3 * * *
docker compose exec -T postgres pg_dump -U postgres telemetry \
  | gzip > /backups/telemetry-$(date +\%F).sql.gz
# then sync /backups to S3/B2/rsync.net
```

Restore: `gunzip -c backup.sql.gz | docker compose exec -T postgres psql -U postgres -d telemetry`.

### 2f. Metabase hardening

- Put it behind the TLS proxy on its own subdomain; require its login.
- It already uses its **own `metabase` database** (separate from your
  counter data), so backups of `telemetry` stay clean.
- Set `MB_SITE_URL` to the public URL and enable HTTPS-only cookies via
  the proxy.

### 2g. Privacy / compliance

The pipeline is zero-PII by construction (allow-listed counter names,
coarse period buckets, integers — no IDs, IPs are not stored, bodies are
never logged) and **opt-out** with a first-run notice. For a public
release, link a short privacy note from the docs stating what's collected
and how to opt out (`dpm telemetry off` / `DO_NOT_TRACK=1`).

---

## Mainnet release checklist

- [ ] VM provisioned; Docker + compose installed
- [ ] `.env` with strong `POSTGRES_PASSWORD`
- [ ] `docker compose up -d --build`; all three healthy
- [ ] Reverse proxy + TLS for `telemetry.yourdomain.tld`; `:8080`/`:3000` bound to loopback
- [ ] `curl https://telemetry.yourdomain.tld/healthz` → `ok`
- [ ] Smoke: POST a sample payload, confirm a row in `counter_period`
- [ ] Metabase admin created; `telemetry` DB added; a starter dashboard saved
- [ ] Nightly `pg_dump` cron → off-host storage
- [ ] Repo variable `TELEMETRY_ENDPOINT` set to the HTTPS `/v1/counters` URL
- [ ] Cut a release tag; download a binary; confirm a counter lands after a day’s use
- [ ] Privacy note published; opt-out verified (`dpm telemetry off`)
