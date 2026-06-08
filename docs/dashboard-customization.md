# Dashboard Customization

Canton DevKit ships a Grafana dashboard that gives you a one-screen
overview of a running LocalNet. The dashboard is provisioned from a
JSON file on disk, so you can extend it, replace it, or restore it
with normal file edits and a stack restart. This guide covers what
ships out of the box, how to add panels, and how to persist your
changes across `localnet down`/`up`.

The dashboard is wired up by the optional **observability** overlay.
Start LocalNet with the overlay enabled to get Prometheus + Grafana
on the side:

```bash
canton-devkit localnet up --name demo --with observability
```

Grafana then runs at `http://localhost:<grafana-port>` (see
`localnet status --name demo` for the port). The default credentials
are printed in the same output.

---

## 1. What ships out of the box

The bundled dashboard lives at
[`assets/grafana/dashboards/canton-localnet.json`](../assets/grafana/dashboards/canton-localnet.json)
and is titled **Canton LocalNet — DApp Developer Overview**. It
refreshes every 10s, defaults to a 15-minute window, and exposes a
single `$instance` template variable backed by
`label_values(up, instance)` so you can scope every panel to one
participant.

| Panel | Type | PromQL | What it tells you |
|---|---|---|---|
| Ledger TPS (5m avg) | stat | `sum(rate(canton_participant_transactions_total{instance=~"$instance"}[5m]))` | Steady-state ledger throughput. Drops here usually point at participant or sequencer back-pressure. |
| Active Participants | stat | `count(up{component="canton", instance=~"$instance"} == 1)` | How many Canton nodes Prometheus can scrape right now. Anything less than expected means a node is unscrapeable. |
| Mediator Approval Latency (p95) | stat | `histogram_quantile(0.95, sum(rate(canton_mediator_approval_duration_bucket{instance=~"$instance"}[5m])) by (le))` | Tail latency of mediator confirmation. The single most useful number for "is the network healthy under load?". |
| Postgres Conn Count | stat | `sum(pg_stat_activity_count{instance=~"$instance"})` | Total live Postgres connections across the stack. A creeping value here is the early signal for a connection-pool leak. |
| Transactions per Second | timeseries | `rate(canton_participant_transactions_total{instance=~"$instance"}[1m])` (per-participant) | Same signal as the TPS stat, broken out per participant so you can see uneven load. |
| JVM Heap Used (per node) | timeseries | `jvm_memory_used_bytes{area="heap", instance=~"$instance"}` | Heap pressure per component. A sawtooth rising baseline is the classic memory-leak shape. |
| Sequencer Message Rate | timeseries | `rate(canton_sequencer_messages_total{instance=~"$instance"}[1m])` | Sequencer-level message rate. Useful for separating ledger-layer slowness from transport-layer stalls. |
| Transaction Processing Latency | timeseries | p50 + p95 of `canton_participant_transaction_duration_bucket` | Per-participant latency at two quantiles. Diverging p50/p95 is the early sign of a stuck transaction. |

All metrics use the `canton_*` and `jvm_*` namespaces emitted by the
Splice participant's Prometheus scrape endpoint. The Postgres metric
comes from a separate exporter that the observability overlay starts
alongside Grafana.

---

## 2. Editing the JSON directly

The dashboard JSON is mounted into the Grafana container by the
provisioner configured in
[`assets/grafana/provisioning/dashboards/canton.yaml`](../assets/grafana/provisioning/dashboards/canton.yaml).
Grafana re-scans this directory every 30 seconds, so edits take
effect without a container restart:

```bash
# 1. Edit the JSON
$EDITOR assets/grafana/dashboards/canton-localnet.json

# 2. Wait up to 30 seconds, then refresh the Grafana tab.
#    No restart needed.
```

If you want the change to apply instantly, restart only the Grafana
container:

```bash
docker compose -p canton-devkit-demo restart grafana
```

`canton-devkit` does not own the Grafana container lifecycle beyond
the overlay; `docker compose restart` against the project name is the
direct path.

### Adding a panel

Append a new entry to the `panels` array. The minimum a panel needs
is an `id` (unique within the dashboard), a `type`, a `title`, a
`gridPos`, and at least one Prometheus `target`. Here is a panel
that surfaces participant indexer lag:

```json
{
  "id": 20,
  "type": "timeseries",
  "title": "Indexer Lag",
  "datasource": "Prometheus",
  "gridPos": { "h": 8, "w": 12, "x": 0, "y": 22 },
  "targets": [
    {
      "expr": "canton_participant_indexer_lag_seconds{instance=~\"$instance\"}",
      "legendFormat": "{{participant}}"
    }
  ]
}
```

Pick an `id` higher than any existing one (the bundled dashboard
goes up to 13). Place the panel below the existing rows by setting
`y` past the last occupied row.

---

## 3. UI edits vs. JSON edits

Grafana lets you edit panels from the browser (the pencil icon on
each panel). With the bundled provisioning config, those UI edits are
**ephemeral by default**: as soon as the provisioner re-syncs from
disk it will overwrite anything you did in the UI.

This is intentional. It keeps the on-disk JSON the source of truth
and avoids the "what's actually deployed?" question that crops up
once people start clicking around in production Grafanas.

If you want persistent UI edits — for exploratory work, or if you
prefer Grafana's panel editor over hand-editing JSON — flip
`allowUiUpdates` to `true` in the provisioner config:

```yaml
# assets/grafana/provisioning/dashboards/canton.yaml
providers:
  - name: canton-localnet
    type: file
    disableDeletion: true
    updateIntervalSeconds: 30
    allowUiUpdates: true   # was false
    options:
      path: /var/lib/grafana/dashboards
```

With `allowUiUpdates: true`, Grafana writes UI edits back into its
own database. The on-disk JSON is still loaded on startup as the
initial state, but subsequent UI changes survive across reloads
until you reset to defaults.

Pick one mode and stick with it. Mixing edits across both surfaces
is how teams end up with two slightly different dashboards and no
clear answer for which is canonical.

---

## 4. Persisting across `down` and `up`

The provisioning directory is bind-mounted into the Grafana
container from the repo, so the JSON survives every `localnet down`
and `localnet up` cycle automatically — the file is on your disk,
not in a container volume.

Two things to know:

1. **The JSON path on the host is the source of truth.** Edit
   `assets/grafana/dashboards/canton-localnet.json` (or drop a new
   `.json` file next to it — the provisioner picks up every JSON in
   that directory). Changes persist with the repo.
2. **UI edits live in a Grafana volume.** When `allowUiUpdates` is
   on, the Grafana SQLite database holds your edits. `localnet down`
   keeps the volume around; `localnet clean --name <n>` removes it.
   If you want UI edits to survive across instances, export them via
   **Dashboard settings → JSON Model → Save** and check the JSON
   into the repo.

### Dropping in your own dashboard

The provisioner loads every `*.json` in
`assets/grafana/dashboards/`. To add a second dashboard alongside
the default, drop a new JSON file next to it:

```bash
cp my-team-dashboard.json assets/grafana/dashboards/
# wait ~30s; refresh Grafana → Dashboards → Browse
```

Each dashboard needs a unique `uid`. The bundled one uses
`canton-localnet-v1`; pick a different value for yours.

---

## 5. Resetting to defaults

Because the on-disk JSON is the source of truth, restoring defaults
is a `git checkout`:

```bash
git checkout -- assets/grafana/dashboards/canton-localnet.json
```

If you had `allowUiUpdates: true` and made UI edits, also wipe the
Grafana volume so its database doesn't override the file on startup:

```bash
docker compose -p canton-devkit-<name> stop grafana
docker volume rm canton-devkit-<name>_grafana-data
canton-devkit localnet restart --name <name>
```

`docker volume ls` will show you the exact volume name for your
instance; the prefix is the compose project name printed by
`localnet status`.

---

## 6. Where the metrics come from

Prometheus scrapes the Canton participants directly. The targets are
declared in the observability overlay's Prometheus config; you can
list them at runtime by browsing to
`http://localhost:<prometheus-port>/targets`.

Metric name conventions used by the bundled dashboard:

| Prefix | Source | Notes |
|---|---|---|
| `canton_participant_*` | Canton participant node | Ledger throughput, indexer lag, transaction durations. |
| `canton_mediator_*` | Canton mediator | Confirmation request durations, approval counters. |
| `canton_sequencer_*` | Canton sequencer | Message rates, batch sizes. |
| `jvm_*` | JVM metric exporter (built into Canton) | Heap, GC, thread counts. Available on every JVM-based node. |
| `pg_*` | Postgres exporter | Per-DB connection counts and statement stats. |
| `up` | Prometheus self | Whether each scrape target is reachable. The `instance` label is the source for the `$instance` template variable. |

If a panel renders as "No data", the fastest debug is to open
Prometheus directly, type the metric name, and see whether the
participant is producing it at all.

---

## 7. Common customizations

### Alert when TPS drops to zero

Edit the **Ledger TPS (5m avg)** stat panel and add an alert rule
(Grafana 9+ alerting). The expression is the same one the panel
uses:

```
sum(rate(canton_participant_transactions_total{instance=~"$instance"}[5m]))
```

Fire when the value is below `0.01` for 5 minutes. The alert lives
inside the dashboard JSON under the panel's `alert` field, so it
persists like any other panel edit.

### Filter every panel to a single participant

The bundled dashboard already exposes `$instance` as a template
variable in the top bar — pick one from the dropdown and every panel
scopes to that participant. If you want a second variable (e.g. one
that filters to a specific `component`), add it to
`templating.list`:

```json
{
  "name": "component",
  "type": "query",
  "datasource": "Prometheus",
  "query": "label_values(up{instance=~\"$instance\"}, component)",
  "refresh": 1,
  "includeAll": true,
  "multi": true
}
```

Then reference it in panel queries with `component=~"$component"`.

### Track DAR upload count

Add a stat panel with:

```
sum(canton_participant_package_count{instance=~"$instance"})
```

Useful when you're iterating on a multi-DAR app and want to see at
a glance how many packages the participant has loaded.

---

## 8. See also

- [docs/getting-started.md](getting-started.md) — installing DevKit
  and starting LocalNet with the observability overlay.
- [docs/telemetry.md](telemetry.md) — what metrics DevKit itself
  emits (separate from Canton's metrics).
- [docs/troubleshooting.md](troubleshooting.md) — common Grafana /
  Prometheus startup issues.
