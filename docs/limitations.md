# Known limitations

Living list of things DevKit does not (yet) do well, with the rationale
and links to follow-up tickets where applicable. Updated as we ship.

## Concurrency / locking

- **Windows: registry locking is process-local only.**
  `internal/registry/lock_windows.go` returns a no-op `Lock()` and
  `withIndexLock` uses a `sync.Mutex` that protects only within the
  same canton-devkit process. Two concurrent `localnet up --name foo`
  invocations on Windows can race past the lock. We print a one-line
  stderr warning on every `Lock` call so users notice. A proper fix
  needs `CreateFileW` + `LockFileEx` via `golang.org/x/sys/windows`;
  out of scope until someone hits this in practice.
  *Linux/macOS use `syscall.Flock` and are unaffected.*

## Splice version pinning

- **(resolved)** *Earlier the catalogue pinned the raw gzip SHA, which
  could drift if GitHub regenerated the source-tarball.* The catalogue
  now pins (a) the git commit SHA (immutable, content-addressable —
  `internal/splice/versions.json`'s `commit` field) and (b) the
  ContentSHA of the extracted `cluster/compose/localnet/` subtree
  (`content_sha` field). The tarball-by-commit URL is byte-stable
  enough; we hash the extracted tree, not the gzip envelope, so a
  gzip-level rewrite (compression-level change, mtime drift) has no
  effect. See `docs/versions.md`.

## Compose env reconstruction

- **`composeContext` rebuilds env from registry state.**
  `down` / `logs` / `creds` need the env that was passed to `up`. We
  reconstruct it from `state.json` so a fresh shell can still operate
  the instance. Any new env var Splice adds in a future release that
  we don't capture in state will silently break operations from a
  fresh shell. Mitigation: integration tests in CI (follow-up ticket).

## Integration testing

- **No CI integration test for `localnet up` against real Splice.**
  Unit tests cover parsers and orchestration well, but the actual
  bring-up flow is never exercised end-to-end in CI. The first
  upstream-contract drift will be found by a user, not by us. Filed
  separately as a follow-up.

## Memory requirements

- **Splice's full stack wants ~12 GB of Docker memory.**
  `cluster/compose/localnet/resource-constraints.yaml` (from
  [canton-network/splice](https://github.com/canton-network/splice))
  sums to canton 4 GB + splice 3 GB +
  postgres 2 GB + console 2 GB + 7 UI services @ 256-512 MB ≈ 12 GB.
  In practice a single instance runs on 7-8 GB because most of those
  limits are headroom. But:

  - **Two concurrent instances exceed 8 GB Docker** → splice in one of
    them gets OOM-restarted by docker, never reaches healthy, and
    `WaitForHealthy` times out at 15 min.
  - **GitHub `ubuntu-latest` runners have 7 GB RAM** — enough for
    `up` to start but Splice's onboarding may not complete. Use a
    larger runner class or self-hosted for the integration job.
  - **Docker Desktop default on macOS is 8 GB.** Bump via Settings →
    Resources before running multi-instance scenarios.

  The preflight check enforces a 4 GB hard floor; the 12 GB
  recommendation is documentation, not a gate — single-instance
  setups on 7-8 GB work fine for most users.

  On timeout, `WaitForHealthy` now dumps the last `docker compose ps`
  snapshot in its error so the stuck service + state are visible
  without re-running anything.

## Platform parity

- **Homebrew formula targets macOS arm64 and Linux x86_64 only.**
  Matches the release matrix. macOS Intel, Linux ARM, and Windows are
  intentionally out of scope.
- **Windows users**: use the standalone zip from GitHub Releases
  rather than DPM until the Windows `.exe` path through DPM is
  verified.
