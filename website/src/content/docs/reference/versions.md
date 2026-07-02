---
title: "Splice Version Catalogue"
description: "How DevKit pins curated Splice LocalNet versions by commit SHA and content hash, discovers upstream tags, and resolves uncurated versions on opt-in."
---

DevKit pins to a **curated** list of Splice versions in
[`internal/splice/versions.json`](https://github.com/bitdynamics-ab/canton-devkit/blob/main/internal/splice/versions.json) so
`localnet up` never composes-up an untested upstream tag.

## What DevKit fetches, and from where

> **Upstream repo:** [`canton-network/splice`](https://github.com/canton-network/splice)
> **Subtree extracted:** `cluster/compose/localnet/`
> **Fetch URL:** `https://github.com/canton-network/splice/archive/<commit-sha>.tar.gz`

That subtree is the canonical Splice LocalNet definition — `compose.yaml`,
`compose.env`, `resource-constraints.yaml`, `conf/`, `docker/`, `env/`.
DevKit downloads only that subtree on cache-miss and verifies it against
the catalogue's `content_sha`; the rest of the Splice source tree is
discarded.

### Not to be confused with `cn-quickstart`

[`digital-asset/cn-quickstart`](https://github.com/digital-asset/cn-quickstart)
is a separate repo that *builds on top of* the same Splice LocalNet to
provide an App-Provider quickstart with a backend service, frontend,
Daml workflows, etc. — see its README for context. DevKit deliberately
fetches the bare LocalNet base from `canton-network/splice` rather than
the App-Provider layer from cn-quickstart, because the lifecycle
commands (`up` / `down` / `status` / `creds` / `logs`) only need the
minimal infrastructure surface. App-Provider workflows are out of scope for DevKit;
users who want them can run cn-quickstart's `make start` on top of a
DevKit-managed LocalNet.

### A note on the upstream URL

GitHub may surface this repo as `hyperledger-labs/splice` in older
documentation (e.g. cn-quickstart's README still uses that name).
That URL redirects to `canton-network/splice` — GitHub's API resolves
both to the same canonical `full_name`, and tag SHAs match. DevKit uses
the canonical name in code and docs.

## Anatomy of a catalogue entry

```json
{
  "tag": "0.6.4",
  "commit": "578b7822d62947763a48334d556aefebc7ffacec",
  "content_sha": "db1e1336dc4e33abe7011a0df29e5becd141d11c84cdf42849e48bf2106066af",
  "size": 137576613,
  "major": "0.6"
}
```

| Field | Source of truth | Why it's pinned |
|---|---|---|
| `tag` | Upstream git tag (or branch label for pre-releases) | User-facing identifier; what `--version` accepts. |
| `commit` | `git ls-remote --tags` at catalogue time (or branch HEAD for pre-releases) | Immutable, content-addressable. DevKit fetches via `archive/<commit>.tar.gz` so a force-pushed tag can't quietly change what `localnet up` installs. |
| `content_sha` | `scripts/compute-tree-sha.sh` | SHA-256 over the extracted `cluster/compose/localnet/` subtree (sorted by path). Stable across upstream gzip-envelope rewrites; this is the authoritative integrity check at fetch time. |
| `size` | byte count of the source-tarball | Informational; used to print a hint before download and to size the in-flight body cap. |
| `major` | first two segments of `tag` (or set manually for branch tags) | Routes to the per-major adapter in `internal/splice/v0X/`. |
| `channel` *(optional)* | catalogue maintainer | `""` / `"stable"` → production-ready; `"alpha"` → opt-in pre-release (Token Standard V2 snapshot etc.). `up` prints a one-line warning when an alpha entry is selected. |
| `image_repo` *(optional)* | catalogue maintainer | Overrides the default Docker image repository. Defaults to `ghcr.io/digital-asset/decentralized-canton-sync/docker`. Set to `ghcr.io/digital-asset/decentralized-canton-sync-dev/docker` for the V2 alpha track. The v06 adapter forwards this as the `IMAGE_REPO` compose env. |

### The alpha channel

DevKit's first alpha entry is the **Token Standard V2** snapshot pointed at by [`token-standard-v2-upcoming`](https://github.com/canton-network/splice/tree/token-standard-v2-upcoming). V2 publishes images to a separate `-dev` ghcr registry (hence the `image_repo` override) and runs only on Canton's *alpha* protocol version (initial protocol 35, alpha-version-support flags). The Canton config side of that requirement is delivered by a separate `--profile tokens-v2` overlay; selecting the alpha catalogue entry without the profile is supported but will not bring up a healthy stack.

**Stability caveat:** the upstream V2 DevNet [is reset and upgraded on a weekly cadence](https://github.com/canton-network/splice/blob/token-standard-v2-upcoming/token-standard/TOKEN_STANDARD_V2_DEVNET.md), so the V2 entry's `commit` will rotate more often than a stable release. Refresh via `scripts/add-splice-version.sh` (modify the script to pass `--ref token-standard-v2-upcoming` for branch-tracking).

## Discovering versions

```sh
dpm localnet versions             # supported + available upstream
dpm localnet versions --offline   # supported only (no network)
dpm localnet versions --format=json
```

Status flags per row:

| Status | Meaning |
|---|---|
| `supported` | Catalogued; upstream pin matches. Safe to use. |
| `drifted` | Catalogued; upstream tag has been force-moved to a different commit. **Security signal** — re-review the catalogue entry before trusting. |
| `available` | Upstream has the tag; not yet in the catalogue. A maintainer can add it via the helper below. |
| `catalogued-only` | In the catalogue, but the online tag listing does not contain the same label. For stable entries this usually means the upstream tag was deleted and should be investigated before removal; branch-backed alpha entries such as `token-standard-v2` can also appear this way until branch/ref-aware status is added. |

## Adding a new version (maintainer flow)

```sh
scripts/add-splice-version.sh 0.6.5
```

The script:
1. Resolves `0.6.5` → commit SHA via the GitHub REST API.
2. Downloads the archive at that commit.
3. Extracts the `cluster/compose/localnet/` subtree.
4. Computes `ContentSHA` via `scripts/compute-tree-sha.sh`.
5. Inserts a new entry into `versions.json` (sorted by tag).
6. Prints the diff. **Does not commit.**

A maintainer then:
- Reviews the diff.
- Bumps `latest_alias` if the new tag should become the default
  `--version latest`.
- Optionally runs the integration test against the new entry before
  merging.
- Commits + pushes.

## Two-layer resolution

DevKit exposes the catalogue as the *default* tier of a two-layer
version model — the curated path stays audited, and an explicit
opt-in unlocks arbitrary upstream tags for prerelease testing.

| Layer | Trigger | Source | ContentSHA | Notes |
|-------|---------|--------|------------|-------|
| 1 — Curated | `--version <tag>` for any tag in `versions.json` (or `--version latest`) | Embedded catalogue | Pinned at catalogue time, verified post-extract | Default. Audited. Offline. |
| 2 — Upstream | `--version <tag> --allow-uncurated` for any tag not in the catalogue | `api.github.com/repos/canton-network/splice/git/refs/tags/<tag>` | Computed on first extract, recorded for future runs | Requires explicit opt-in. Network on first call. Cached at `~/.canton-devkit/cache/resolved-versions.json`. |

Layer 2 trades audit for flexibility: it lets a user spin up a
`0.7.0-alpha.4` LocalNet without waiting for a catalogue PR, but
DevKit can't promise the bits were tested against this release.
Orchestrators print a one-line "Using uncurated Splice tag" warning
on the layer-2 path so the user is never surprised.

Because layer 2 covers the prerelease use case, the catalogue is
strictly a curated-by-humans surface — entries are only added by a
maintainer, never by automation.

## Why not just point at the latest tag?

Three reasons the catalogue is curated:

1. **Reproducibility.** A user running `localnet up --version 0.6.4`
   today must get exactly the bits that were tested when the entry
   was added. Tag-only resolution would let `0.6.4` quietly point at
   different code if the upstream tag is moved.

2. **Surface area control.** Splice ships pre-release tags
   (`next-cilr`, etc.) and partial-release tags that aren't intended
   for downstream consumption. DevKit doesn't aim to support every
   commit that happens to land in the repo.

3. **Adapter routing.** DevKit ships per-major adapters
   (`internal/splice/v05/`, `v06/`). A new major version (e.g. `0.7.x`)
   needs a corresponding adapter before it can be added — the script
   leaves `major` blank for non-N.N.N tags so a maintainer notices.

## Why the content SHA, not the tarball hash

Pinning the gzip-tarball SHA would be brittle: GitHub regenerates
source-tarballs lazily and the gzip metadata can drift, so the same
source tree can yield different tarball hashes over time. The catalogue
therefore pins the commit SHA in the URL plus a ContentSHA over the
extracted tree — a complete integrity check that is stable across gzip
envelope rewrites.
