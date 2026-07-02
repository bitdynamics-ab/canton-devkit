# Known limitations

Things DevKit does not (yet) do well, with the rationale and
workarounds where applicable. This list is updated as limitations are
resolved.

## Instance naming

- **`--name` must be a DNS label.** Names are validated against RFC 1123:
  1-63 chars of lowercase `[a-z0-9-]`, must start and end with `[a-z0-9]`.
  Uppercase, underscores, and leading/trailing hyphens are rejected.
  DNS-label form was chosen so the same name is safe to embed as a
  hostname in a future `{service}.{instance}.localhost` routing model
  without a second translation step. The single source of truth lives in
  `internal/registry/state.go` (`ValidateName`); the CLI layer delegates.
  *Migration:* instances created with an older release that still
  allowed uppercase or underscore names (e.g. `MyStack`, `my_stack`)
  must be torn down with that older binary and re-created under a
  DNS-label name.

## Concurrency / locking

- **(resolved)** Registry locking is now a real cross-process lock on
  every platform. On Windows both the fail-fast per-instance lock and
  the blocking index read-modify-write lock go through
  `windows.LockFileEx` (`internal/registry/lock_windows.go`,
  `internal/registry/index_lock_windows.go`); Linux/macOS use
  `syscall.Flock`. The OS releases the lock when the handle closes or
  the process exits, so there is no stale lock file to recover.

## Splice version pinning

- **(resolved)** The catalogue pins (a) the git commit SHA (immutable,
  content-addressable — `internal/splice/versions.json`'s `commit`
  field) and (b) the ContentSHA of the extracted
  `cluster/compose/localnet/` subtree (`content_sha` field). The hash
  covers the extracted tree, not the gzip envelope, so a gzip-level
  rewrite by GitHub (compression-level change, mtime drift) has no
  effect. See [versions.md](./versions.md).

## Container image pinning

- **Splice container images are pulled by mutable ghcr tags, not
  digests.** The catalogue pins the source TREE (commit SHA +
  post-extract ContentSHA), but Splice's compose references every image
  through a single shared `IMAGE_TAG` variable
  (`image: "${IMAGE_REPO}canton:${IMAGE_TAG}"`,
  `${IMAGE_REPO}splice-app:${IMAGE_TAG}`, the web UIs, …). Because one
  variable addresses ~6 distinct images, per-image `@sha256:` digests
  cannot be injected via the compose env — a single digest can't pin
  six different images.

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
  `down` / `logs` / `creds` need the env that was passed to `up`.
  DevKit reconstructs it from `state.json` so a fresh shell can still
  operate the instance. Any new env var a future Splice release adds
  that is not captured in state will silently break operations from a
  fresh shell.

## Integration testing

- **No CI integration test for `localnet up` against real Splice.**
  Unit tests cover parsers and orchestration well, but the actual
  bring-up flow is not yet exercised end-to-end in CI, so drift in the
  upstream Splice compose contract may first surface at runtime rather
  than in CI.

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
    larger runner class or a self-hosted runner for CI jobs that
    bring up LocalNet.
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

## <a name="shared-observability-stack"></a>Observability: transitional dual stack

DevKit runs a host-level shared Prometheus + Grafana stack — one
stack serves every running LocalNet via file-based service discovery,
refcounted by target file. See
[docs/observability.md](observability.md#stack-topology--host-shared-with-a-transitional-per-instance-overlay)
for the topology.

- **Each observability-enabled instance still *also* runs a
  per-instance Prometheus + Grafana overlay** alongside the shared
  stack, so while running it has **two** Prometheus and **two** Grafana
  containers — roughly **~600 MiB** of duplicated overhead per extra
  environment.
- **Why it's kept (for now).** The per-instance overlay is a deliberate
  fallback: both the CLI and the Web UI read shared-first and fall back
  to the per-instance Prometheus when the shared stack isn't up, and the
  per-instance scrape uses in-network service DNS (`canton:10013`)
  rather than `host.docker.internal`, so it works on any platform
  regardless of the Linux `host-gateway` mapping.
- **Planned.** Gating the per-instance overlay off (to drop the
  duplication) is deferred until the shared-only path is end-to-end
  validated on a native Linux Docker host. The runtime toggle funnels
  through a single neutral function
  (`internal/localnet.SetObservability`), so removing the overlay is
  additive rather than a rewrite of both surfaces.
