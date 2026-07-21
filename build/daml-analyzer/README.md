# daml-analyzer image

Packages Certora's [daml-analyzer](https://github.com/Certora/daml-analyzer)
(Apache-2.0) — a static analyzer for cross-package interactions in a compiled
Daml package — as a pinned, reproducible container image. The devkit runs it
via `docker run` (see `internal/analyzer`), so there is no host Java dependency
and nothing heavy in git; the image lives in a registry.

- Upstream commit: `143a7e2a9f24db5ea9bbd7680809d596d1151bcb`
- License: Apache-2.0 (see `LICENSE`)
- Default image ref: `ghcr.io/bitdynamics-ab/daml-analyzer:0.1.0-143a7e2`
  (matches `analyzer.DefaultImage`; override with `DAML_ANALYZER_IMAGE`).

The `Dockerfile` is multi-stage: it builds the fat jar from the pinned commit
in a JDK+sbt stage and ships it on a slim JRE. The entrypoint is the analyzer,
so arguments are the in-container dar path plus flags.

## Build locally

    make analyzer-image                 # builds DAML_ANALYZER_IMAGE (default tag)

or directly:

    docker build -t ghcr.io/bitdynamics-ab/daml-analyzer:0.1.0-143a7e2 build/daml-analyzer

## Run manually

    docker run --rm --network none -v "$PWD/foo.dar:/in/foo.dar:ro" \
      ghcr.io/bitdynamics-ab/daml-analyzer:0.1.0-143a7e2 /in/foo.dar -f json

## Publish

The `.github/workflows/analyzer-image.yml` workflow builds and pushes to GHCR
on manual dispatch. Bump the pinned commit here, in the `Dockerfile`
(`ANALYZER_COMMIT`), and in `analyzer.DefaultImage` together, then re-publish.
For arm64 as well as amd64, build multi-arch with buildx:

    docker buildx build --platform linux/amd64,linux/arm64 --push \
      -t ghcr.io/bitdynamics-ab/daml-analyzer:0.1.0-143a7e2 build/daml-analyzer
