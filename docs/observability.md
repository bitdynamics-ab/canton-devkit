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
