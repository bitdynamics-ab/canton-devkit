---
name: canton-ci-localnet
description: Run app tests against a throwaway Canton LocalNet in CI (up → test → teardown). Use when the user wants to wire LocalNet into GitHub Actions / GitLab CI.
---

# LocalNet in CI

Stand up a disposable Canton LocalNet for integration tests, then tear
it down — using canton-devkit's deterministic exit codes and
readiness-wait.

## When to use
The user asks to "test against Canton in CI", "add LocalNet to my
pipeline", or "run integration tests in GitHub Actions/GitLab".

## Safe workflow (the five beats)

1. **Install** the pinned release (verify checksum against `SHA256SUMS`).
2. **Preflight**: `canton-devkit localnet doctor` — fail fast if the
   runner's Docker host isn't ready.
3. **Start** (blocks until healthy; no manual sleep):
   ```
   canton-devkit localnet up --name ci --version <splice-tag>
   ```
4. **Export + test**:
   ```
   canton-devkit localnet env --name ci >> "$GITHUB_ENV"
   canton-devkit localnet dar upload ./dist/app.dar --name ci   # optional
   # run your tests against the exported endpoints
   ```
5. **Teardown in an always-run step** so failures still clean up:
   ```
   canton-devkit localnet clean --name ci --force
   ```

## Guardrails
- Put teardown in `if: always()` (GitHub) / `after_script` (GitLab) so
  a failed test never leaves dangling containers/volumes.
- Pin BOTH the devkit release and the Splice version for reproducible
  CI.
- The runner needs Docker Engine + Compose v2 and ~8 GB RAM for Docker.

Copy-pasteable workflow files live in `examples/ci/` in the repo.
