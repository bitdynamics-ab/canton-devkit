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
  without a second translation step. Name validation is centralized so
  every surface enforces the same rule.
  *Migration:* instances created with an older release that still
  allowed uppercase or underscore names (e.g. `MyStack`, `my_stack`)
  must be torn down with that older binary and re-created under a
  DNS-label name.

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
  (`image_digests`) and, on a later `up` of the SAME version,
  WARNs if a digest changed — i.e. a mutable ghcr tag was republished
  under you (`restart` reuses the existing containers, so no re-check
  happens there). This is a warning,
  not a gate (a digest can legitimately change if you manually re-pull),
  and it's best-effort (a capture failure just skips the check). True
  digest-pinning at pull time would need upstream Splice to expose
  per-image digest variables in its compose.

## Compose env reconstruction

- **`composeContext` rebuilds env from registry state.**
  `down` / `restart` / `pause` / `clean` need the env that was passed
  to `up`; DevKit reconstructs it from `state.json` so a fresh shell
  can still operate the instance. (`logs` and `creds` read the
  registry/containers directly and need no env reconstruction.)
  Any new env var a future Splice release adds
  that is not captured in state will silently break operations from a
  fresh shell.

## Integration testing

- **Integration coverage for `localnet up` against real Splice runs
  nightly, not on every PR.** Unit tests cover parsers and
  orchestration on every PR, but the end-to-end bring-up flow runs
  only nightly (and on PRs labeled `run-integration`) via
  `.github/workflows/integration.yml`, so drift in the upstream
  Splice compose contract can land up to a day before CI notices.

## Memory requirements

- **Splice's full stack wants ~12 GB of Docker memory.**
  `cluster/compose/localnet/resource-constraints.yaml` (from
  [canton-network/splice](https://github.com/canton-network/splice))
  sums to canton 4 GB + splice 3 GB +
  postgres 2 GB + console 2 GB + 7 UI services @ 256-512 MB (plus
  nginx/swagger-ui) ≈ 13 GB of limits — DevKit's coded recommendation
  is 12 GB. In practice a single instance runs on 7-8 GB because most of those
  limits are headroom. But:

  - **Two concurrent instances exceed 8 GB Docker** → splice in one of
    them gets OOM-restarted by docker, never reaches healthy, and
    `WaitForHealthy` times out at 25 min.
  - **GitHub `ubuntu-latest` runners have 16 GB RAM on public repos
    but only 8 GB on private repos** — on private-repo runners `up`
    starts but Splice's onboarding may not complete; use a larger
    runner class or a self-hosted runner there.
  - **Docker Desktop defaults to 50% of host memory** (8 GB on a
    16 GB Mac). Bump via Settings → Resources before running
    multi-instance scenarios.

  The preflight check enforces a per-version hard floor: 8 GB for the
  0.6 line (0.6.3, 0.6.4/`latest`, the V2 alpha — and any uncurated
  0.6.x tag, which inherits the strictest catalogued floor for its
  major), 4 GB for 0.5.18 and only for tags whose major has no
  catalogued entry. The 12 GB figure is the coded recommendation
  threshold (`recommended_memory_bytes`) — below it preflight WARNs
  but does not refuse.

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

## Observability: transitional dual stack

DevKit runs a host-level shared Prometheus + Grafana stack — one
stack serves every running LocalNet via file-based service discovery,
refcounted by target file. See
[Observability](observability.md#stack-topology--host-shared-with-a-transitional-per-instance-overlay)
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
- **Removal pending validation.** The per-instance overlay stays enabled
  until the shared-only path is validated end-to-end on a native Linux
  Docker host. When that validation completes, the overlay can be gated
  off without changing the CLI or Web UI observability commands.
