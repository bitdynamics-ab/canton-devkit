# CI examples

Copy-pasteable workflows that run your Daml/Canton app tests against a
throwaway Canton LocalNet managed by `canton-devkit`.

| File | Platform |
|---|---|
| [`github-actions.yml`](./github-actions.yml) | GitHub Actions |
| [`gitlab-ci.yml`](./gitlab-ci.yml) | GitLab CI |

## The pattern

Every example follows the same five beats:

1. **Install** the pinned `canton-devkit` release (verify checksum).
2. **`localnet doctor`** — fail fast if the runner's Docker host isn't ready.
3. **`localnet up --name ci`** — start LocalNet. This **blocks until the
   stack is healthy** (or exits non-zero with a timeout message), so you
   never need a manual `sleep`.
4. **`localnet env`** — export Ledger/JSON/admin endpoints + party IDs
   into the job environment, then run your tests against them. Optionally
   `dar upload` your package first.
5. **Teardown** in an always-run step (`if: always()` / `after_script`)
   so the LocalNet is cleaned up even when tests fail — no dangling
   containers or volumes on self-hosted runners.

## Two contracts this relies on

- **Readiness wait**: `localnet up` returns only when LocalNet is healthy
  or it fails — your test step never races a half-started stack.
- **Deterministic exit codes**: any non-zero exit fails the job. See the
  command reference for the exit-code table.

## Requirements

- A runner with **Docker Engine + Compose v2** reachable.
- Enough resources for the Splice stack (~8 GB RAM for Docker, ~20 GB
  disk). GitHub-hosted `ubuntu-latest` works; for heavier matrices use a
  self-hosted runner.

Pin both `DEVKIT_VERSION` and `SPLICE_VERSION` (a curated tag — see
`canton-devkit localnet versions`) for reproducible CI.
