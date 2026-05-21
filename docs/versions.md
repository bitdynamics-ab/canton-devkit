# Splice version catalogue

DevKit pins to a **curated** list of Splice versions in
[`internal/splice/versions.json`](../internal/splice/versions.json) so
`localnet up` never composes-up an untested upstream tag.

## What we fetch, and from where

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
the App-Provider layer from cn-quickstart, because we want the minimal
infrastructure surface for our lifecycle (`up` / `down` / `status` /
`creds` / `logs`). App-Provider workflows are out of scope for DevKit;
users who want them can run cn-quickstart's `make start` on top of a
DevKit-managed LocalNet.

### A note on the upstream URL

GitHub may surface this repo as `hyperledger-labs/splice` in older
documentation (e.g. cn-quickstart's README still uses that name).
That URL redirects to `canton-network/splice` — GitHub's API resolves
both to the same canonical `full_name`, and tag SHAs match. We use the
canonical name in code and docs.

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
| `tag` | Upstream git tag | User-facing identifier; what `--version` accepts. |
| `commit` | `git ls-remote --tags` at catalogue time | Immutable, content-addressable. We fetch via `archive/<commit>.tar.gz` so a force-pushed tag can't quietly change what `localnet up` installs. |
| `content_sha` | `scripts/compute-tree-sha.sh` | SHA-256 over the extracted `cluster/compose/localnet/` subtree (sorted by path). Stable across upstream gzip-envelope rewrites; this is the authoritative integrity check at fetch time. |
| `size` | byte count of the source-tarball | Informational; used to print a hint before download and to size the in-flight body cap. |
| `major` | first two segments of `tag` | Routes to the per-major adapter in `internal/splice/v0X/`. |

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
| `available` | Upstream has the tag; not yet in our catalogue. A maintainer can add it via the helper below. |
| `catalogued-only` | We catalogue it; upstream no longer has it (tag was deleted). Investigate before removing. |

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

A reviewer then:
- Verifies the diff.
- Bumps `latest_alias` if the new tag should become the default
  `--version latest`.
- Optionally runs the integration test against the new entry before
  merging.
- Commits + pushes.

## Automatic refresh

`.github/workflows/refresh-versions.yml` runs every Monday at 06:00 UTC:
- Enumerates upstream tags via the GitHub API.
- Filters to N.N.N semver (skips pre-release artifacts like `next-cilr`).
- Runs the maintainer script for any tag not in the catalogue.
- Opens a PR with the diff. A human reviewer merges or closes.

Manual trigger: GitHub Actions → "Refresh Splice versions" → Run workflow.

## Why not just point at the latest tag?

Three reasons we curate:

1. **Reproducibility.** A user running `localnet up --version 0.6.4`
   today must get exactly the bits that were tested when the entry
   was added. Tag-only resolution would let `0.6.4` quietly point at
   different code if the upstream tag is moved.

2. **Surface area control.** Splice ships pre-release tags
   (`next-cilr`, etc.) and partial-release tags that aren't intended
   for downstream consumption. We don't want to support every commit
   that happens to land in the repo.

3. **Adapter routing.** DevKit ships per-major adapters
   (`internal/splice/v05/`, `v06/`). A new major version (e.g. `0.7.x`)
   needs a corresponding adapter before it can be added — the script
   leaves `major` blank for non-N.N.N tags so a maintainer notices.

## What changed in this refactor

Pre-2026-05, the catalogue lived in `versions.go` as a Go map literal
and pinned both the gzip-tarball SHA and the ContentSHA. The gzip
hash was brittle: GitHub regenerates source-tarballs lazily and the
gzip metadata can drift. The current model drops the gzip hash; the
commit SHA in the URL + ContentSHA over the extracted tree is the full
integrity check, and it's stable across gzip envelope rewrites.
