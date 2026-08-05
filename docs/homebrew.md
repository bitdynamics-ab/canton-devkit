# Homebrew install

`canton-devkit` ships a Homebrew formula for macOS (Apple Silicon) and
Linux (x86_64). The formula lives in the dedicated tap repository
[`bitdynamics-ab/homebrew-canton-devkit`](https://github.com/bitdynamics-ab/homebrew-canton-devkit),
following the standard Homebrew tap layout. Release tarballs (and their
`SHA256SUMS`) are published on this repository's
[GitHub Releases](https://github.com/bitdynamics-ab/canton-devkit/releases);
the formula's `url` fields point here.

This source repository does not keep a `Formula/` directory. The tap
holds only the Homebrew formula; APT metadata lives under `apt/` in this
repository, and the canonical `install.sh` lives at the repo root.

## Install

Homebrew requires formulae to live in a tap (installing a formula from
a URL or a bare file path is no longer supported), so install via the
tap:

```sh
brew tap bitdynamics-ab/canton-devkit
brew install bitdynamics-ab/canton-devkit/canton-devkit
```

> Note: the formula's `version` + `sha256` fields are rewritten
> automatically by the release workflow on every release tag (see
> below), so the tap always installs the latest published release. There
> is no `--HEAD` install path — the formula installs prebuilt release
> artifacts only.

## Upgrade

After a new release is published and the formula is updated:

```sh
brew update
brew upgrade canton-devkit
```

## How the formula stays in sync

This is **automatic** on every release tag (`v*`). `.github/workflows/release.yml`:

1. Builds and publishes the per-platform tarballs and a single GNU
   `sha256sum` manifest named `SHA256SUMS` to a GitHub Release on
   `bitdynamics-ab/canton-devkit`.
2. Reads the `darwin_arm64` and `linux_amd64` digests out of
   `dist/SHA256SUMS`, rewrites the `version` + two `sha256` fields of the
   tap's `Formula/canton-devkit.rb`, and commits the change back via the
   GitHub contents API (commit message
   `chore: bump Homebrew formula to <tag>`).

No maintainer action is required for a normal release.

If the automated formula bump fails, edit
`Formula/canton-devkit.rb` in a local clone of the tap: set `version` to
the bare semver and replace the two `sha256` lines with the
`darwin_arm64` / `linux_amd64` digests from that release's `SHA256SUMS`
on canton-devkit, then commit and push to the tap's `main`.

## Smoke test

Current Homebrew rejects bare-path formula installs ("Homebrew requires
formulae to be in a tap"), so test a not-yet-pushed formula bump by
tapping the local clone. `brew tap` clones the git repo, so commit the
bump locally in `../homebrew-canton-devkit` first, then:

```sh
brew tap bitdynamics-ab/canton-devkit ../homebrew-canton-devkit
brew install bitdynamics-ab/canton-devkit/canton-devkit
brew test canton-devkit
canton-devkit localnet --help
```

After pushing, the plain `brew tap bitdynamics-ab/canton-devkit &&
brew install canton-devkit` form verifies the published tap.

`brew test canton-devkit` runs the formula's `test do` block, which
executes `canton-devkit localnet --help` and asserts the LocalNet
command tree is reachable.

## What's not supported (yet)

- **Windows** — Homebrew doesn't target Windows. Use the standalone
  artifact from the
  [canton-devkit releases page](https://github.com/bitdynamics-ab/canton-devkit/releases).
- **Linux ARM** — not in the release matrix. Could be added in a
  follow-up if there's demand.
- **macOS Intel** — same; the project's compatibility matrix is
  Apple Silicon only.
