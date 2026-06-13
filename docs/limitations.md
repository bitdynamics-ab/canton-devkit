# Known limitations

Living list of things DevKit does not (yet) do well, with the rationale
and links to follow-up tickets where applicable. Updated as we ship.

## Instance naming

- **`--name` must be a DNS label.** Names are validated against RFC 1123:
  1-63 chars of lowercase `[a-z0-9-]`, must start and end with `[a-z0-9]`.
  Uppercase, underscores, and leading/trailing hyphens are rejected.
  We chose DNS-label form so the same name is safe to embed as a
  hostname in the future `{service}.{instance}.localhost` routing model
  without a second translation step. Single source of truth lives in
  `internal/registry/state.go` (`ValidateName`); the CLI layer delegates.
  *Migration:* pre-PR-#20 instances created with uppercase or underscore
  names (e.g. `MyStack`, `my_stack`) must be torn down with the old
  binary and re-created under a DNS-label name.

## Concurrency / locking

- **(resolved)** *Earlier the Windows registry lock was a no-op and
  `withIndexLock` was a process-local `sync.Mutex`, so two concurrent
  `localnet up --name foo` invocations on Windows could race past the
  lock.* Both now take real cross-process locks via
  `windows.LockFileEx` (`internal/registry/lock_windows.go` uses
  `LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY` for the
  fail-fast per-instance lock; `internal/registry/index_lock_windows.go`
  uses the blocking `LOCKFILE_EXCLUSIVE_LOCK` for the index
  read-modify-write). The OS releases the lock when the handle closes
  or the process exits, so there is no stale lock file to recover. This
  uses `golang.org/x/sys/windows`, already a direct dependency (e.g.
  `internal/localnet/snapshot/diskspace_windows.go`).
  *Linux/macOS use `syscall.Flock`; behaviour is now equivalent across
  platforms.*

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

## Container image pinning

- **Splice container images are pulled by mutable ghcr tags, not
  digests.** The catalogue pins the source TREE (commit SHA +
  post-extract ContentSHA), but Splice's compose references every image
  through a single shared `IMAGE_TAG` variable
  (`image: "${IMAGE_REPO}canton:${IMAGE_TAG}"`,
  `${IMAGE_REPO}splice-app:${IMAGE_TAG}`, the web UIs, …). Because one
  variable addresses ~6 distinct images, we can't inject per-image
  `@sha256:` digests via the compose env — a single digest can't pin six
  different images.

  Instead DevKit VERIFIES post-up: after services are healthy it records
  each running image's content digest (image ID) in `state.json`
  (`image_digests`) and, on a later `up`/`restart` of the SAME version,
  WARNs if a digest changed — i.e. a mutable ghcr tag was republished
  under you. See `internal/localnet/image_digests.go`. This is a warning,
  not a gate (a digest can legitimately change if you manually re-pull),
  and it's best-effort (a capture failure just skips the check). True
  digest-pinning at pull time would need upstream Splice to expose
  per-image digest variables in its compose.

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
