# Observability — Prometheus + Grafana

The `--profile observability` flag on `canton-devkit localnet up`
adds two containers to the compose project:

- **Prometheus** scrapes `canton:10013` and `splice:10013` (both
  containers ship `/app/monitoring.conf` enabling the built-in
  OTel reporter — see `assets/compose/prometheus.yml`).
- **Grafana** auto-provisions the `canton-localnet` dashboard from
  `assets/grafana/dashboards/canton-localnet.json`.

The host ports for both UIs are allocated at `up` time and
persisted in the registry so re-up preserves bookmarked URLs.

## Metric naming convention

The live Splice 0.6.4 Prometheus surfaces **three** metric prefix
families. Earlier versions of the dashboard and `internal/metricsq`
used a `canton_*` prefix that does NOT exist upstream — those
queries silently returned no data. The audit notes below pin the
current convention so future panels stay aligned.

Probe used to ground-truth the names:

```
PROM_PORT=$(canton-devkit localnet status --name <inst> --format json \
  | jq -r '.endpoints[] | select(.label=="prometheus_ui") | .port')
curl -s "http://localhost:${PROM_PORT}/api/v1/label/__name__/values" \
  | jq -r '.data[]' > /tmp/all-metrics.txt
```

597 metric names total on a healthy obs-enabled LocalNet. Prefix
families:

| Prefix              | Source                                          | Example                                                            |
| ------------------- | ----------------------------------------------- | ------------------------------------------------------------------ |
| `daml_*`            | Daml participant / mediator / sequencer (OTel)  | `daml_participant_api_indexer_updates`                             |
| `jvm_*`             | OTel JVM runtime instrumentation                | `jvm_memory_used_bytes{jvm_memory_type="heap"}`                    |
| `db_client_*`       | HikariCP pool stats from the JVM apps           | `db_client_connections_usage{state="used"}`                        |
| `cn_*` / `sv_*`     | Splice super-validator + Scan business metrics  | `cn_db_storage_general_executor_exectime_duration_seconds_bucket`  |
| `splice_*`          | Splice triggers + domain params                 | `splice_trigger_latency_duration_seconds_bucket`                   |

**There is no `canton_*` prefix.** Panels that need a Canton-level
synchronizer / participant metric should use `daml_*` (where the
metric is emitted by the Daml participant) or `daml_sequencer_*` /
`daml_mediator_*` (where it comes from the protocol layer).

### Substitute mapping (what changed)

| Old (non-existent)                              | Replacement                                                              | Notes                                                                                 |
| ----------------------------------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------- |
| `canton_participant_transactions_total`         | `daml_participant_api_indexer_updates`                                   | Counter of ledger updates ingested by the indexer.                                    |
| `canton_mediator_approval_duration_bucket`      | `daml_sequencer_client_submissions_sequencing_duration_seconds_bucket`   | No mediator-approval histogram is exposed; sequencer-client send→sequenced is the closest end-to-end latency. |
| `canton_sequencer_messages_total`               | `daml_sequencer_block_events_total`                                      | Counter of block events emitted by the sequencer.                                     |
| `canton_participant_transaction_duration_bucket`| `daml_sequencer_client_submissions_sequencing_duration_seconds_bucket`   | Same substitute as the mediator one.                                                  |
| `jvm_memory_used_bytes{area="heap"}`            | `jvm_memory_used_bytes{jvm_memory_type="heap"}`                          | OTel JVM instrumentation uses `jvm_memory_type`, not the legacy micrometer `area`.    |
| `pg_stat_activity_count`                        | `db_client_connections_usage{state="used"}`                              | No postgres exporter is scraped; HikariCP pool usage is the apps'-side view of the same number. |

## Smoke test (drift guard)

`internal/metricsq/smoke_test.go` (build tag `integration`) queries
every `Headline*` in `SummaryQueries` against a live Prometheus and
fails if any returns 0 results — the only way to catch silent
metric-name drift when Splice updates.

Run it locally:

```
canton-devkit localnet up --name metric-audit --profile observability
PROM_PORT=$(canton-devkit localnet status --name metric-audit --format json \
  | jq -r '.endpoints[] | select(.label=="prometheus_ui") | .port')
METRICSQ_SMOKE_PROM=http://localhost:${PROM_PORT} \
  go test -tags=integration -run TestSummaryQueries_LiveProm \
  ./internal/metricsq/
canton-devkit localnet clean --name metric-audit --force
```

The test is **skipped** when `METRICSQ_SMOKE_PROM` is unset, so
`go test -tags=integration ./...` on a box with no LocalNet stays
green. Set the env var in CI to fail-fast.

## Toggling observability on a running instance

You don't have to decide about metrics at `up` time. Both surfaces
expose a runtime toggle that brings Prometheus + Grafana up (or down)
on an already-running instance **without restarting Canton**:

- **Web UI** — the Metrics screen's "Enable observability now" button
  (`POST /api/instances/{name}/observability`).
- **CLI** — `dpm localnet observability enable|disable|status`:

  ```
  # Turn both sidecars on for a running instance
  canton-devkit localnet observability enable --name demo

  # Just Prometheus (no Grafana image / RAM)
  canton-devkit localnet observability enable --name demo --prometheus

  # Turn Grafana back off, leave Prometheus scraping
  canton-devkit localnet observability disable --name demo --grafana

  # Report what's running + the dashboard URL (works while stopped too)
  canton-devkit localnet observability status --name demo --format json
  ```

Both surfaces call the **same** neutral orchestration
(`internal/localnet.SetObservability`) — there is no second
docker-compose code path that could drift. With neither `--prometheus`
nor `--grafana`, the verb acts on both sidecars (the legacy umbrella
semantics); pass one flag to operate on a single component.

## Survives a down → up cycle

The profile set an instance was brought up with — whether via
`--profile observability` at create time or via the runtime toggle — is
persisted in the registry (`state.json`'s `profiles` field). A later
`down` + `up` (or the Web UI **Restart**) **re-enables the same
profiles automatically**; you do not have to re-pass `--profile`. An
explicit `--profile` on the re-up still wins (replaces, doesn't merge),
so you can deliberately drop observability. This closes the prior gap
where Prometheus/Grafana silently vanished on every restart even though
the stable-port contract kept the bookmarked Grafana URL alive.

## Stack topology — host-shared, with a transitional per-instance overlay

A single **host-level** Prometheus + Grafana (#39) serves every running
LocalNet, fulfilling the original proposal (line 188). It runs as its own
compose project (`canton-devkit-observability`), independent of any
instance's lifecycle. Each observability-enabled instance publishes its
canton/splice `:10013` metrics ports on `127.0.0.1:<ephemeral>` and writes
a Prometheus **file_sd** target file (`host.docker.internal:<hostport>`,
labelled `instance` + `component`); the shared Prometheus discovers
instances from those files. The **number of target files is the refcount**:
the stack starts on the first instance's `up` and is torn down when the
last instance's `down`/`clean` removes its target file. Register+ensure and
deregister+teardown each run under a dedicated **shared-stack lock** so a
concurrent `up` and `down` of different instances can't race the stack into
a "registered but torn down" state, and orphaned target files (left by a
crash) are reconciled against the registry index. On native Linux the
Prometheus service carries `extra_hosts: ["host.docker.internal:host-gateway"]`
so the loopback-published ports resolve; on Docker Desktop the name is
auto-provided.

**Transitional dual stack (known trade-off).** Each observability-enabled
instance currently *also* still runs its own per-instance Prometheus +
Grafana overlay alongside the shared stack — so while running, an obs
instance has **two** Prometheus and **two** Grafana containers. This is a
deliberate, kept fallback: both the CLI and the Web UI read **shared-first**
and fall back to the per-instance Prometheus when the shared stack isn't
up, and the per-instance scrape uses in-network service DNS
(`canton:10013`) rather than `host.docker.internal`, so it works on any
platform regardless of the Linux `host-gateway` mapping. Gating the
per-instance overlay off (to drop the duplication) is deferred until the
shared-only path can be end-to-end validated on a native Linux Docker host
— see [docs/limitations.md](limitations.md#shared-observability-stack). The
extra resource cost (a second Prometheus+Grafana per instance) is the price
of that fallback on a dev machine; it carries no correctness impact.
