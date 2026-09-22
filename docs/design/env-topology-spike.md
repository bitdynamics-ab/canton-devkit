# Environment topology compiler — Phase-0 spike

**Goal:** compile a developer-described topology (multiple validators, multiple
synchronizers) into an *upstream* Splice LocalNet via generated config + a
compose override — without forking the Splice stack. This note records what the
Phase-0 spike established before any YAML/schema/UI work.

**Verdict: feasible.** The upstream LocalNet is built for exactly this kind of
extension, and a generated config + compose override validates against the real
upstream compose.

## What the upstream LocalNet actually is

Read from `canton-network/splice` `cluster/compose/localnet/` (interface is
identical across 0.6–0.8):

- **One `canton` container** runs every logical Canton node (participants,
  sequencers, mediators). It **bind-mounts** its config — nothing is baked:
  `${LOCALNET_DIR}/conf/canton/app.conf → /app/app.conf`, plus per-role dirs.
- `app.conf` defines reusable HOCON anchors (`_participant`, `_storage`) and
  assembles the topology purely by `include file("/app/app-provider/on/app.conf")`
  … Each participant is just:
  ```hocon
  canton.participants.<name> = ${_participant} {
    storage.config.properties.databaseName = participant-<name>
    ledger-api.port = ...
    admin-api.port  = ...
  }
  ```
  The bundled role configs also define the `app-sequencer` / `app-mediator`
  (synchronizer nodes) the same way.
- Postgres provisions databases dynamically from `CREATE_DATABASE_*` env vars.
- A **multi-synchronizer bootstrap already exists**: services
  `multi-sync-startup` (extends `console`, `MULTI_SYNC=true`) and
  `multi-sync-ready`, driven by `conf/console/app-synchronizer.sc`. This is the
  mechanism to *generalize*, not invent.

Everything mounts from `${LOCALNET_DIR}`, which DevKit's adapter already sets —
so pointing it at a generated environment dir feeds our config to the unmodified
upstream compose.

## What the spike proved (validated)

`internal/environment` generates, from a hard-coded 2-participant topology:

- participant HOCON blocks reusing the upstream `_participant` anchor, appended
  to the mounted `app.conf`;
- a `compose.override.yaml` that publishes each participant's ports and adds its
  `CREATE_DATABASE_*` env.

Layering that override over the **real** upstream `compose.yaml` +
`resource-constraints.yaml`:

```
docker compose -f <cache>/compose.yaml -f <cache>/resource-constraints.yaml \
               -f <envdir>/compose.override.yaml config   → VALID
```

The merged config carries the injected participant's ports, the DB-creation env,
and the canton container mounting our generated `app.conf`. The upstream cache is
never touched. (Reproduce with `DEVKIT_ENV_SPIKE_DOCKER=1 go test
./internal/environment/...`.)

## Not yet validated (the real risk, per plan §36)

`docker compose config` validates the *compose* layer only. Still unproven:

- **HOCON parses and Canton boots** with generated participants (needs the
  canton image + a real `up`).
- **Synchronizer generation**: a generated `abc-sequencer`/`abc-mediator` and
  bootstrap. The plan's §11–12 generalization of `app-synchronizer.sc` is the
  next hard part.
- **Connect + enable multi-sync** across arbitrary participants, and resolving
  `type: base` to the LocalNet's existing global synchronizer (§13).
- **Validator App onboarding** — the highest risk: the Splice Validator App may
  assume `app-provider` / `app-user` beyond the obvious config files. Phase 0
  exists to flush these out.

## Injection strategy chosen

Generated environment dir under `~/.canton-devkit/environments/<name>/` holding
the mount sources (a copy of upstream `conf`/`docker`/`env` with generated
additions) + `compose.override.yaml`. `LOCALNET_DIR` points at it; upstream
`compose.yaml` is referenced unmodified via `-f`. This keeps the downstream delta
to *generated config + a small override* (plan §18–19).

## Next (Milestone A)

1. Generated participant HOCON — **done (spike)**.
2. Boot the 2-participant env for real (`up`), confirm Ledger/Admin APIs.
3. Generated Validator App HOCON + onboarding (secret, party hint, DB, JWT).
4. Deterministic port blocks + generated DBs + local auth.
5. Generated sequencer/mediator for one `type: local` synchronizer.
6. Generalize the `app-synchronizer.sc` bootstrap (connect + enable multi-sync)
   with no `app-provider`/`app-user` assumptions.
7. A/B/C flagship (`examples/environments/consortium-lab.yaml`).

Only after that: the `devkit.yaml` parser, `validate`, `plan`, and `env up/down`.
